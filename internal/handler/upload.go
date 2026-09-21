package handler

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/willvar/dofs"

	"domus/internal/auth"
	"domus/internal/filekind"
	"domus/internal/middleware"
	"domus/internal/model"
	"domus/internal/store"
)

var uploadControlFields = map[string]struct{}{
	"path": {}, "file_name": {}, "file_size": {}, "content_type": {},
	"conflict_strategy": {}, "client_instance_id": {}, "internal": {},
	"dek": {}, "expected_generation": {}, "names": {},
	"upload_id": {}, "content_hash": {}, "encrypted_size": {},
	"media_width": {}, "media_height": {}, "media_duration": {},
	"thumbnail_upload_id": {},
}

var (
	uploadInitFields = fieldSet(
		"path", "file_name", "file_size", "content_type", "conflict_strategy",
		"client_instance_id", "internal", "dek", "expected_generation",
	)
	uploadConflictFields = fieldSet("path", "names")
	uploadCompleteFields = fieldSet(
		"upload_id", "content_hash", "encrypted_size", "media_width",
		"media_height", "media_duration", "thumbnail_upload_id",
	)
)

const (
	maxUploadPathBytes       = 4096
	maxUploadNameBytes       = 255
	maxUploadContentType     = 255
	maxClientInstanceIDBytes = 128
	maxConflictNames         = 1000
	maxBrowserThumbnailSize  = 4 * 1024 * 1024
)

func decodeUploadControlFields(body []byte) (map[string]json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return nil, err
	}
	if fields == nil {
		return nil, errors.New("upload control message must be a JSON object")
	}
	return fields, nil
}

func rejectUploadPayloadFields(fields map[string]json.RawMessage, allowed map[string]struct{}) error {
	for field := range fields {
		if _, ok := allowed[field]; !ok {
			return fmt.Errorf("unsupported upload control field %q", field)
		}
	}
	return nil
}

func validUploadPath(value string) bool {
	return len(value) <= maxUploadPathBytes && !strings.ContainsRune(value, '\x00')
}

func validUploadName(value string) bool {
	return value != "" && value != "." && value != ".." && len(value) <= maxUploadNameBytes &&
		!strings.ContainsAny(value, "/\\\x00")
}

func validHexDigest(value string) bool {
	if value == "" {
		return true
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32
}

const (
	// presignExpiry is the lifetime of presigned upload URLs.
	presignExpiry              = 1 * time.Hour
	directUploadReservationTTL = presignExpiry + 30*time.Minute
)

const (
	// directUploadPartSize leaves enough room for chunk alignment while keeping
	// every non-final multipart part above S3's 5 MiB minimum. A 5 MiB buffer
	// can hold only 79 complete encrypted 64 KiB chunks (about 4.94 MiB), which
	// S3 rejects when completing a multipart upload.
	directUploadPartSize int64 = 8 * 1024 * 1024
	maxMultipartParts          = 10_000
	s3MinimumPartSize    int64 = 5 * 1024 * 1024
	s3MaximumPartSize    int64 = 5 * 1024 * 1024 * 1024
)

// encryptedFileSize returns the on-disk size of a file after AES-256-GCM
// chunked encryption:  5-byte header + plaintext + 28 bytes per chunk.
func encryptedFileSize(plainSize int64) int64 {
	if plainSize <= 0 {
		return 5
	}
	chunkPlain := int64(auth.DefaultChunkSize) // 65536
	const overhead int64 = 28                  // 12 nonce + 16 tag
	numChunks := (plainSize + chunkPlain - 1) / chunkPlain
	return 5 + plainSize + numChunks*overhead
}

// uploadPartSizeFor returns the multipart part size for a plaintext file.
// Parts split only on encrypted chunk boundaries, so the size grows with the
// object to keep the total part count within the protocol ceiling. Files stay
// uploadable up to the object-store limit instead of a product-level cap.
func uploadPartSizeFor(plainSize int64) (int64, error) {
	if plainSize < 0 {
		return 0, errors.New("invalid file size")
	}
	encryptedChunk := int64(auth.NonceSize + auth.DefaultChunkSize + auth.TagSize)
	partSize := directUploadPartSize
	encrypted := encryptedFileSize(plainSize)
	if needed := (encrypted + maxMultipartParts - 1) / maxMultipartParts; needed > partSize {
		partSize = needed
		if remainder := partSize % encryptedChunk; remainder != 0 {
			partSize += encryptedChunk - remainder
		}
	}
	if partSize > s3MaximumPartSize || encryptedMultipartPartCount(plainSize, partSize) > maxMultipartParts {
		return 0, errors.New("file exceeds the multipart object size ceiling")
	}
	return partSize, nil
}

// encryptedMultipartPartCount returns the number of OSS multipart parts the
// browser uploader will actually produce. Parts can only split between
// encrypted chunks, so this can be larger than ceil(encryptedSize/partSize).
func encryptedMultipartPartCount(plainSize, partSize int64) int {
	if partSize <= 0 {
		return 1
	}

	parts := 1
	used := int64(5) // encryption header lives in the first part
	chunkPlain := int64(auth.DefaultChunkSize)
	fullChunkSize := int64(auth.NonceSize + auth.DefaultChunkSize + auth.TagSize)

	placeChunk := func(chunkSize int64) {
		if used+chunkSize > partSize {
			parts++
			used = 0
		}
		used += chunkSize
	}

	fullChunks := plainSize / chunkPlain
	for i := int64(0); i < fullChunks; i++ {
		placeChunk(fullChunkSize)
	}

	if rem := plainSize % chunkPlain; rem > 0 {
		placeChunk(int64(auth.NonceSize) + rem + int64(auth.TagSize))
	}

	return parts
}

// authoritativeCompleteParts validates the parts reported by OSS and converts
// them to the shape required by CompleteMultipartUpload. Completion deliberately
// does not trust browser-supplied ETags: browsers can only read that response
// header when a bucket exposes it through CORS, while the server can always get
// the authoritative value from ListParts.
func authoritativeCompleteParts(parts []store.PartInfo, maximumCount int, expectedSize int64) ([]store.CompletePart, bool) {
	if len(parts) == 0 || len(parts) > maximumCount || expectedSize <= 0 {
		return nil, false
	}

	completeParts := make([]store.CompletePart, len(parts))
	var totalSize int64
	for i, part := range parts {
		if part.PartNumber != i+1 || strings.TrimSpace(part.ETag) == "" || part.Size <= 0 || part.Size > s3MaximumPartSize {
			return nil, false
		}
		if i < len(parts)-1 && part.Size < s3MinimumPartSize {
			return nil, false
		}
		if totalSize > expectedSize-part.Size {
			return nil, false
		}
		totalSize += part.Size
		completeParts[i] = store.CompletePart{
			PartNumber: part.PartNumber,
			ETag:       part.ETag,
		}
	}
	if totalSize != expectedSize {
		return nil, false
	}
	return completeParts, true
}

// handleUploadDispatch routes POST /file/upload to init, complete, or conflict check.
func (h *Handler) handleUploadDispatch(c *fiber.Ctx) error {
	fields, err := decodeUploadControlFields(c.Body())
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid_upload_control_message"})
	}
	allowed := uploadInitFields
	_, complete := fields["upload_id"]
	_, conflict := fields["names"]
	if complete {
		allowed = uploadCompleteFields
	} else if conflict {
		allowed = uploadConflictFields
	}
	if err := rejectUploadPayloadFields(fields, allowed); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid_upload_control_message"})
	}
	var peek struct {
		UploadID string   `json:"upload_id"`
		Names    []string `json:"names"`
	}
	if err := c.BodyParser(&peek); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid_request"})
	}
	if complete {
		return h.handleUploadComplete(c)
	}
	if conflict {
		return h.handleUploadConflictCheck(c)
	}
	return h.handleUploadInit(c)
}

// ── helpers ──────────────────────────────────────────────────────────────────

func nextAvailableName(name string, usedNames map[string]struct{}) string {
	base, ext := splitFileName(name)
	index := 1
	for {
		candidate := fmt.Sprintf("%s (%d)%s", base, index, ext)
		if _, exists := usedNames[candidate]; !exists {
			return candidate
		}
		index++
	}
}

func splitFileName(name string) (string, string) {
	dot := strings.LastIndex(name, ".")
	if dot <= 0 {
		return name, ""
	}
	return name[:dot], name[dot:]
}

// ── conflict check (unchanged) ──────────────────────────────────────────────

func (h *Handler) handleUploadConflictCheck(c *fiber.Ctx) error {
	var body struct {
		Path  string   `json:"path"`
		Names []string `json:"names"`
	}
	if err := c.BodyParser(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid_request"})
	}
	if !validUploadPath(body.Path) || len(body.Names) == 0 || len(body.Names) > maxConflictNames {
		return c.Status(400).JSON(fiber.Map{"error": "invalid_request"})
	}
	for _, name := range body.Names {
		if !validUploadName(name) {
			return c.Status(400).JSON(fiber.Map{"error": "invalid_request"})
		}
	}

	session := c.Locals("session").(*model.Session)
	dirPath := body.Path
	if dirPath != "" && !strings.HasSuffix(dirPath, "/") {
		dirPath += "/"
	}

	resolvedDir, err := middleware.ResolvePath(c, dirPath)
	if err != nil {
		return err
	}

	allFiles, _ := h.Repos.Files.ListAllChildren(session.UserID, resolvedDir)
	existingByName := make(map[string]*model.FileRecord, len(allFiles))
	for i := range allFiles {
		existingByName[allFiles[i].Name] = &allFiles[i]
	}

	var conflicts []fiber.Map
	for _, name := range body.Names {
		if existing, ok := existingByName[name]; ok {
			conflicts = append(conflicts, fiber.Map{
				"name":          existing.Name,
				"size":          existing.Size,
				"is_dir":        existing.IsDir,
				"last_modified": existing.UpdatedAt,
			})
		}
	}

	return c.JSON(fiber.Map{"conflicts": conflicts})
}

// ── init ─────────────────────────────────────────────────────────────────────

func (h *Handler) handleUploadInit(c *fiber.Ctx) error {
	var body struct {
		Path               string `json:"path"`
		FileName           string `json:"file_name"`
		FileSize           int64  `json:"file_size"`
		ContentType        string `json:"content_type"`
		ConflictStrategy   string `json:"conflict_strategy"`
		ClientInstanceID   string `json:"client_instance_id"`
		Internal           bool   `json:"internal"`
		DEK                string `json:"dek"`
		ExpectedGeneration int64  `json:"expected_generation"`
	}
	if err := c.BodyParser(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid_request"})
	}
	if !validUploadPath(body.Path) || !validUploadName(body.FileName) ||
		len(body.ContentType) > maxUploadContentType ||
		len(body.ClientInstanceID) > maxClientInstanceIDBytes ||
		body.ExpectedGeneration < 0 {
		return c.Status(400).JSON(fiber.Map{"error": "invalid_request"})
	}
	if body.ConflictStrategy != "" && body.ConflictStrategy != "replace" && body.ConflictStrategy != "rename" {
		return c.Status(400).JSON(fiber.Map{"error": "invalid_conflict_strategy"})
	}

	session := c.Locals("session").(*model.Session)
	if h.FileSystem == nil {
		return c.Status(503).JSON(fiber.Map{"error": "dofs_unavailable"})
	}
	fileKey, err := hex.DecodeString(strings.TrimSpace(body.DEK))
	if err != nil || len(fileKey) != dofs.KeySize {
		return c.Status(400).JSON(fiber.Map{"error": "invalid_dek"})
	}
	defer dofs.Clear(fileKey)

	if body.FileSize < 0 {
		return c.Status(400).JSON(fiber.Map{"error": "invalid_file_size"})
	}

	ownerID := session.UserID
	dirPath := body.Path
	fileName := body.FileName
	filePath := ""
	resolvedPath := ""
	replace := false
	var existing *model.FileRecord
	if dirPath != "" && !strings.HasSuffix(dirPath, "/") {
		dirPath += "/"
	}
	filePath = dirPath + fileName
	if !body.Internal && isProtectedMutationRoot(filePath) {
		return c.Status(400).JSON(fiber.Map{"error": "invalid_path"})
	}
	resolveUploadPath := middleware.ResolvePath
	if body.Internal {
		resolveUploadPath = middleware.ResolveInternalPath
	}
	resolvedPath, err = resolveUploadPath(c, filePath)
	if err != nil {
		return err
	}

	existing, err = h.Repos.Files.Get(ownerID, resolvedPath)
	if err == nil && existing != nil {
		switch body.ConflictStrategy {
		case "replace":
			if existing.IsDir || existing.Status != "ready" {
				return c.Status(409).JSON(fiber.Map{"error": "replace_target_busy"})
			}
			replace = true
		case "rename":
			resolvedDir, dirErr := resolveUploadPath(c, dirPath)
			if dirErr != nil {
				return dirErr
			}
			allFiles, _ := h.Repos.Files.ListAllChildren(ownerID, resolvedDir)
			usedNames := make(map[string]struct{})
			for _, f := range allFiles {
				usedNames[f.Name] = struct{}{}
			}
			fileName = nextAvailableName(fileName, usedNames)
			filePath = dirPath + fileName
			resolvedPath, err = resolveUploadPath(c, filePath)
			if err != nil {
				return err
			}
			existing = nil
		default:
			return c.Status(409).JSON(fiber.Map{
				"error": "file_already_exists",
				"existing": fiber.Map{
					"name":          existing.Name,
					"size":          existing.Size,
					"is_dir":        existing.IsDir,
					"last_modified": existing.UpdatedAt,
				},
			})
		}
	}
	if body.ExpectedGeneration > 0 && (existing == nil || existing.Generation != body.ExpectedGeneration) {
		currentGeneration := int64(0)
		if existing != nil {
			currentGeneration = existing.Generation
		}
		return c.Status(409).JSON(fiber.Map{
			"error": "generation_conflict", "current_generation": currentGeneration,
		})
	}
	if body.Internal {
		if err := h.ensureParentDirRecords(ownerID, resolvedPath, false); err != nil {
			return c.Status(500).JSON(fiber.Map{"error": "create_parent_failed"})
		}
	}

	// Replacements retain the current file key so an atomic generation change
	// does not also require a key-identity migration.
	effectiveKey := fileKey
	var retainedKey []byte
	if replace {
		if existing == nil || existing.WrappedDEK == "" {
			return c.Status(409).JSON(fiber.Map{"error": "replace_target_unencrypted"})
		}
		ownerKEK, keyErr := h.loadUserKEK(ownerID)
		if keyErr != nil {
			return c.Status(500).JSON(fiber.Map{"error": "unwrap_failed"})
		}
		wrapped, decodeErr := hex.DecodeString(existing.WrappedDEK)
		if decodeErr == nil {
			retainedKey, keyErr = auth.UnwrapDEK(ownerKEK, wrapped)
		} else {
			keyErr = decodeErr
		}
		dofs.Clear(ownerKEK)
		if keyErr != nil || len(retainedKey) != dofs.KeySize {
			dofs.Clear(retainedKey)
			return c.Status(500).JSON(fiber.Map{"error": "unwrap_failed"})
		}
		defer dofs.Clear(retainedKey)
		effectiveKey = retainedKey
	}

	// Encrypted file geometry
	partSize, err := uploadPartSizeFor(body.FileSize)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "file_too_large"})
	}

	uploadID := uuid.New().String()
	direct, err := h.FileSystem.PrepareDirectUpload(
		c.UserContext(), ownerID, uploadID, resolvedPath, body.FileSize,
		effectiveKey, replace, directUploadReservationTTL,
	)
	if err != nil {
		switch {
		case errors.Is(err, dofs.ErrAlreadyExists):
			return c.Status(409).JSON(fiber.Map{"error": "file_already_exists"})
		case errors.Is(err, dofs.ErrConflict):
			return c.Status(409).JSON(fiber.Map{"error": "upload_conflict"})
		default:
			return c.Status(500).JSON(fiber.Map{"error": "reserve_upload_failed"})
		}
	}
	// The reservation captures the current generation atomically in DOFS. A
	// FUSE write may race the earlier path lookup, so optimistic browser edits
	// must compare against this authoritative value before any OSS upload is
	// created.
	if body.ExpectedGeneration > 0 && direct.ExpectedGeneration != body.ExpectedGeneration {
		_ = h.FileSystem.AbortDirectUpload(c.UserContext(), ownerID, uploadID)
		return c.Status(409).JSON(fiber.Map{
			"error": "generation_conflict", "current_generation": direct.ExpectedGeneration,
		})
	}
	ossUploadID, err := h.Store.CreateMultipartUpload(direct.ObjectKey)
	if err != nil {
		_ = h.FileSystem.AbortDirectUpload(c.UserContext(), ownerID, uploadID)
		return c.Status(500).JSON(fiber.Map{"error": "create_multipart_failed"})
	}

	// Task + application upload projection. Ciphertext target and wrapped key
	// remain owned by the durable DOFS reservation.
	taskID := ""
	if !body.Internal {
		taskID = uuid.New().String()
		if err := h.Repos.Tasks.Create(ownerID, taskID, "upload", fileName); err != nil {
			_ = h.Store.AbortMultipartUpload(direct.ObjectKey, ossUploadID)
			_ = h.FileSystem.AbortDirectUpload(c.UserContext(), ownerID, uploadID)
			return c.Status(500).JSON(fiber.Map{"error": "create_task_failed"})
		}
	}
	contentType := filekind.ContentType(fileName, body.ContentType)
	if err := h.Repos.Files.CreateUpload(ownerID, uploadID, taskID, ossUploadID, resolvedPath, fileName, body.FileSize, contentType, body.ClientInstanceID); err != nil {
		if taskID != "" {
			_ = h.Repos.Tasks.Delete(taskID)
		}
		_ = h.Store.AbortMultipartUpload(direct.ObjectKey, ossUploadID)
		_ = h.FileSystem.AbortDirectUpload(c.UserContext(), ownerID, uploadID)
		return c.Status(500).JSON(fiber.Map{"error": "record_upload_failed"})
	}
	if h.Hub != nil {
		h.notifyParentDir(ownerID, resolvedPath)
	}

	return c.JSON(fiber.Map{
		"upload_id":     uploadID,
		"task_id":       taskID,
		"file_name":     fileName,
		"oss_upload_id": ossUploadID,
		"part_size":     partSize,
		"total_parts":   encryptedMultipartPartCount(body.FileSize, partSize),
		"dek":           hex.EncodeToString(effectiveKey),
	})
}

// ── presign (batch) ──────────────────────────────────────────────────────────

func (h *Handler) authorizedUpload(userID, uploadID string) (*model.FileRecord, error) {
	if h.FileSystem == nil {
		return nil, errors.New("DOFS unavailable")
	}
	return h.FileSystem.GetUpload(userID, uploadID)
}

func (h *Handler) handleUploadPresign(c *fiber.Ctx) error {
	uploadID := c.Query("upload_id", "")
	if uploadID == "" {
		return c.Status(400).JSON(fiber.Map{"error": "upload_id required"})
	}
	start, _ := strconv.Atoi(c.Query("start", "1"))
	count, _ := strconv.Atoi(c.Query("count", "100"))
	if start < 1 {
		start = 1
	}
	maxBatch := h.Config.OSS.MaxPresignBatch
	if maxBatch <= 0 {
		maxBatch = 100
	}
	if count < 1 || count > maxBatch {
		count = maxBatch
	}

	session := c.Locals("session").(*model.Session)
	record, err := h.authorizedUpload(session.UserID, uploadID)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "upload_not_found"})
	}
	if record.Status != "uploading" {
		return c.Status(409).JSON(fiber.Map{"error": "upload_not_active"})
	}
	if h.FileSystem == nil {
		return c.Status(503).JSON(fiber.Map{"error": "dofs_unavailable"})
	}
	direct, err := h.FileSystem.GetDirectUpload(c.UserContext(), record.UserID, uploadID)
	if err != nil || direct.State != dofs.ExternalUploadActive || direct.ObjectKey != record.StorageKey() {
		return c.Status(409).JSON(fiber.Map{"error": "upload_reservation_missing"})
	}
	partSize, partErr := uploadPartSizeFor(record.Size)
	if partErr != nil {
		return c.Status(409).JSON(fiber.Map{"error": "file_too_large"})
	}
	totalParts := encryptedMultipartPartCount(record.Size, partSize)
	if start > totalParts {
		return c.Status(400).JSON(fiber.Map{"error": "invalid_part_range"})
	}
	if remaining := totalParts - start + 1; count > remaining {
		count = remaining
	}
	if _, err := h.FileSystem.RefreshDirectUpload(
		c.UserContext(), record.UserID, uploadID, directUploadReservationTTL,
	); err != nil {
		return c.Status(409).JSON(fiber.Map{"error": "upload_reservation_expired"})
	}
	_ = h.Repos.Files.TouchUpload(uploadID, time.Now())

	type partURL struct {
		PartNumber   int    `json:"part_number"`
		PresignedURL string `json:"presigned_url"`
	}
	parts := make([]partURL, 0, count)
	for i := 0; i < count; i++ {
		pn := start + i
		url, err := h.Store.PresignedUploadPart(record.StorageKey(), record.OSSUploadID, pn, presignExpiry)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"error": "presign_failed"})
		}
		parts = append(parts, partURL{PartNumber: pn, PresignedURL: url})
	}

	return c.JSON(fiber.Map{
		"parts":      parts,
		"expires_in": int(presignExpiry.Seconds()),
	})
}

// ── complete ─────────────────────────────────────────────────────────────────

func (h *Handler) handleUploadComplete(c *fiber.Ctx) error {
	var body struct {
		UploadID          string  `json:"upload_id"`
		ContentHash       string  `json:"content_hash"`
		EncryptedSize     int64   `json:"encrypted_size"`
		MediaWidth        int     `json:"media_width"`
		MediaHeight       int     `json:"media_height"`
		MediaDuration     float64 `json:"media_duration"`
		ThumbnailUploadID string  `json:"thumbnail_upload_id"`
	}
	if err := c.BodyParser(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid_request"})
	}
	if _, err := uuid.Parse(body.UploadID); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid_upload_id"})
	}
	if !validHexDigest(body.ContentHash) {
		return c.Status(400).JSON(fiber.Map{"error": "invalid_content_hash"})
	}
	if body.MediaWidth < 0 || body.MediaHeight < 0 || body.MediaDuration < 0 {
		return c.Status(400).JSON(fiber.Map{"error": "invalid_media_metadata"})
	}
	if body.ThumbnailUploadID != "" {
		if _, err := uuid.Parse(body.ThumbnailUploadID); err != nil {
			return c.Status(400).JSON(fiber.Map{"error": "invalid_thumbnail_upload_id"})
		}
	}

	session := c.Locals("session").(*model.Session)
	record, err := h.authorizedUpload(session.UserID, body.UploadID)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "upload_not_found"})
	}
	ownerID := record.UserID
	if record.Status == "ready" {
		current, currentErr := h.Repos.Files.GetByID(ownerID, record.ID)
		if currentErr != nil {
			return c.Status(500).JSON(fiber.Map{"error": "resolve_published_file_failed"})
		}
		// A previous request may have published the generation and committed the
		// Domus projection before its response or acknowledgement was delivered.
		// Retrying completion also finishes that final cleanup step.
		_ = h.FileSystem.AcknowledgeDirectUpload(c.UserContext(), ownerID, body.UploadID)
		return c.JSON(fiber.Map{"ok": true, "generation": current.Generation})
	}
	if h.FileSystem == nil {
		return c.Status(503).JSON(fiber.Map{"error": "dofs_unavailable"})
	}
	direct, err := h.FileSystem.GetDirectUpload(c.UserContext(), ownerID, body.UploadID)
	if err != nil {
		return c.Status(409).JSON(fiber.Map{"error": "upload_reservation_missing"})
	}

	expectedEncryptedSize := encryptedFileSize(record.Size)
	if body.EncryptedSize <= 0 || body.EncryptedSize != expectedEncryptedSize {
		return c.Status(400).JSON(fiber.Map{
			"error":         "invalid_encrypted_size",
			"expected_size": expectedEncryptedSize,
			"actual_size":   body.EncryptedSize,
		})
	}

	completionPartSize, partErr := uploadPartSizeFor(record.Size)
	if partErr != nil {
		return c.Status(409).JSON(fiber.Map{"error": "file_too_large"})
	}
	expectedTotalParts := encryptedMultipartPartCount(record.Size, completionPartSize)
	if direct.State == dofs.ExternalUploadActive && record.OSSUploadID != "" {
		// Completing an S3 multipart upload consumes its upload ID. If Domus
		// crashes immediately afterwards, retry by recognizing the immutable
		// object instead of getting stuck on NoSuchUpload from ListParts.
		objectAlreadyComplete := false
		if existing, headErr := h.Store.HeadObject(direct.ObjectKey); headErr == nil {
			if existing.Size != expectedEncryptedSize {
				return c.Status(409).JSON(fiber.Map{"error": "reserved_object_conflict"})
			}
			objectAlreadyComplete = true
		}
		if !objectAlreadyComplete {
			listedParts, listErr := h.Store.ListParts(direct.ObjectKey, record.OSSUploadID)
			if listErr != nil {
				return c.Status(500).JSON(fiber.Map{"error": "verify_parts_failed"})
			}
			completeParts, valid := authoritativeCompleteParts(listedParts, expectedTotalParts, expectedEncryptedSize)
			if !valid {
				return c.Status(400).JSON(fiber.Map{"error": "invalid_parts"})
			}
			if completeErr := h.Store.CompleteMultipartUpload(direct.ObjectKey, record.OSSUploadID, completeParts); completeErr != nil {
				return c.Status(500).JSON(fiber.Map{"error": "complete_multipart_failed"})
			}
		}
	} else if direct.State == dofs.ExternalUploadActive {
		return c.Status(409).JSON(fiber.Map{"error": "multipart_state_missing"})
	}

	head, err := h.Store.HeadObject(direct.ObjectKey)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "verify_failed"})
	}
	if head.Size != expectedEncryptedSize {
		return c.Status(400).JSON(fiber.Map{
			"error":         "size_mismatch",
			"expected_size": expectedEncryptedSize,
			"actual_size":   head.Size,
		})
	}

	// Resolve both sides before publishing the new generation. The source
	// record still exposes the previous generation's thumbnail at this point;
	// the internal thumbnail upload, when present, is already committed.
	var previousSource *model.FileRecord
	if candidate, previousErr := h.Repos.Files.Get(ownerID, record.Path); previousErr == nil && candidate.Status == "ready" {
		previousSource = candidate
	}
	var thumbnailRecord *model.FileRecord
	if body.ThumbnailUploadID != "" {
		candidate, thumbnailErr := h.Repos.Files.GetUpload(ownerID, body.ThumbnailUploadID)
		expectedName := "thumb_" + body.UploadID + ".webp"
		expectedPath := "/.domus/thumbnails/" + expectedName
		if thumbnailErr != nil || candidate.Status != "ready" || candidate.ID == record.ID ||
			candidate.Path != expectedPath || candidate.Name != expectedName ||
			candidate.ContentType != "image/webp" || candidate.Size <= 0 || candidate.Size > maxBrowserThumbnailSize {
			return c.Status(409).JSON(fiber.Map{"error": "thumbnail_upload_not_ready"})
		}
		thumbnailRecord = candidate
	}

	published, err := h.FileSystem.CommitDirectUpload(c.UserContext(), ownerID, body.UploadID)
	if err != nil {
		if errors.Is(err, dofs.ErrConflict) {
			return c.Status(409).JSON(fiber.Map{"error": "generation_conflict"})
		}
		return c.Status(500).JSON(fiber.Map{"error": "publish_upload_failed"})
	}
	current, err := h.Repos.Files.GetByID(ownerID, int64(published.Inode))
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "resolve_published_file_failed"})
	}
	record.Path, record.Parent, record.Name = current.Path, current.Parent, current.Name

	contentType := filekind.ContentType(record.Name, record.ContentType)
	if err := h.Repos.Files.Upsert(
		ownerID, record.Path, record.Name, false,
		published.Size, contentType, body.ContentHash,
	); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "record_finalize_failed"})
	}

	if previousSource != nil && previousSource.ThumbnailKey != "" &&
		(thumbnailRecord == nil || previousSource.ThumbnailKey != thumbnailRecord.StorageKey()) {
		// Thumbnail reclamation is best-effort: a transient cleanup failure may
		// leave encrypted garbage, but must not make the source upload unusable.
		if h.cleanupThumbnailStorage(ownerID, previousSource) == nil {
			h.reclaimNamespaceBestEffort(ownerID)
		}
	}

	// A replacement upload commits a new source generation: every rendition of
	// the previous plaintext is now stale.
	if previousSource != nil {
		h.cleanupRenditions(ownerID, previousSource.ID)
	}

	if thumbnailRecord != nil {
		if err := h.Repos.Files.UpdateThumbnail(
			ownerID, record.Path,
			thumbnailRecord.StorageKey(), thumbnailRecord.WrappedDEK,
			body.MediaWidth, body.MediaHeight, body.MediaDuration,
		); err != nil {
			return c.Status(500).JSON(fiber.Map{"error": "thumbnail_attach_failed"})
		}
	} else if body.MediaWidth > 0 || body.MediaHeight > 0 {
		_ = h.Repos.Files.UpdateThumbnail(
			ownerID, record.Path,
			"", "",
			body.MediaWidth, body.MediaHeight, body.MediaDuration,
		)
	}
	if err := h.Repos.Files.UpdateStatus(body.UploadID, "ready"); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "record_status_failed"})
	}

	if record.TaskID != "" {
		_ = h.Repos.Tasks.UpdateStatus(record.TaskID, "completed")
	}

	if err := h.FileSystem.AcknowledgeDirectUpload(c.UserContext(), ownerID, body.UploadID); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "acknowledge_upload_failed"})
	}

	if h.Hub != nil {
		parent := parentDirOf(record.Path)
		if parent != "" {
			appPath := toAppPath(parent, "")
			h.Hub.PushDirChanged(ownerID, parent, appPath, "refresh")
		}
	}

	h.Audit.LogFromCtx(c, "file_upload", record.Path, record.Name, "completed", 0)
	return c.JSON(fiber.Map{"ok": true, "generation": published.Generation})
}

func (h *Handler) handleUploadHeartbeat(c *fiber.Ctx) error {
	var body struct {
		UploadID string `json:"upload_id"`
	}
	if err := c.BodyParser(&body); err != nil || strings.TrimSpace(body.UploadID) == "" {
		return c.Status(400).JSON(fiber.Map{"error": "upload_id required"})
	}
	session := c.Locals("session").(*model.Session)
	uploadID := strings.TrimSpace(body.UploadID)
	record, err := h.authorizedUpload(session.UserID, uploadID)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "upload_not_found"})
	}
	if record.Status != "uploading" {
		return c.Status(409).JSON(fiber.Map{"error": "upload_not_active"})
	}
	if h.FileSystem == nil {
		return c.Status(503).JSON(fiber.Map{"error": "dofs_unavailable"})
	}
	if _, err := h.FileSystem.RefreshDirectUpload(
		c.UserContext(), record.UserID, uploadID, directUploadReservationTTL,
	); err != nil {
		return c.Status(409).JSON(fiber.Map{"error": "upload_reservation_expired"})
	}
	if err := h.Repos.Files.TouchUpload(uploadID, time.Now()); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "heartbeat_failed"})
	}
	return c.JSON(fiber.Map{"ok": true})
}

func (h *Handler) handleUploadCancel(c *fiber.Ctx) error {
	var body struct {
		UploadID string `json:"upload_id"`
		TaskID   string `json:"task_id"`
		Reason   string `json:"reason"`
		Status   string `json:"status"`
	}
	if err := c.BodyParser(&body); err != nil || strings.TrimSpace(body.UploadID) == "" {
		return c.Status(400).JSON(fiber.Map{"error": "upload_id required"})
	}

	uploadID := strings.TrimSpace(body.UploadID)
	taskID := strings.TrimSpace(body.TaskID)
	reason := strings.TrimSpace(body.Reason)
	status := strings.TrimSpace(body.Status)
	if status != "failed" {
		status = "cancelled"
	}
	session := c.Locals("session").(*model.Session)
	if h.FileSystem == nil {
		return c.Status(503).JSON(fiber.Map{"error": "dofs_unavailable"})
	}
	record, err := h.FileSystem.GetUpload(session.UserID, uploadID)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "upload_not_found"})
	}
	if record.OSSUploadID != "" {
		_ = h.Store.AbortMultipartUpload(record.StorageKey(), record.OSSUploadID)
	}
	_ = h.FileSystem.AbortDirectUpload(c.UserContext(), record.UserID, uploadID)
	if taskID == "" {
		taskID = record.TaskID
	}
	if taskID != "" {
		_ = h.Repos.Tasks.UpdateStatus(taskID, status)
	}
	if h.Hub != nil {
		h.notifyParentDir(record.UserID, record.Path)
	}
	if reason != "" {
		h.Audit.LogFromCtx(c, "file_upload_abort", record.Path, reason, status, 0)
	}
	return c.JSON(fiber.Map{"ok": true})
}

func (h *Handler) handleUploadCleanup(c *fiber.Ctx) error {
	var body struct {
		ClientInstanceID string `json:"client_instance_id"`
	}
	_ = c.BodyParser(&body)
	session := c.Locals("session").(*model.Session)
	clientInstanceID := strings.TrimSpace(body.ClientInstanceID)
	cutoff := time.Now().Add(-15 * time.Second)
	records, err := h.Repos.Files.CancelUploadsForOtherInstances(session.UserID, clientInstanceID, cutoff)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "cleanup_failed"})
	}
	for _, record := range records {
		if record.OSSUploadID != "" {
			_ = h.Store.AbortMultipartUpload(record.StorageKey(), record.OSSUploadID)
		}
		if h.FileSystem != nil {
			_ = h.FileSystem.AbortDirectUpload(c.UserContext(), session.UserID, record.UploadID)
		}
		if record.TaskID != "" {
			_ = h.Repos.Tasks.UpdateStatus(record.TaskID, "cancelled")
		}
		if h.Hub != nil {
			h.notifyParentDir(session.UserID, record.Path)
		}
	}
	return c.JSON(fiber.Map{"ok": true, "count": len(records)})
}

// ── status ───────────────────────────────────────────────────────────────────

func (h *Handler) handleUploadStatus(c *fiber.Ctx) error {
	uploadID := c.Query("upload_id", "")
	if uploadID == "" {
		return c.Status(400).JSON(fiber.Map{"error": "upload_id required"})
	}

	session := c.Locals("session").(*model.Session)
	record, err := h.authorizedUpload(session.UserID, uploadID)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "upload_not_found"})
	}

	// Query completed parts from OSS
	var parts []store.PartInfo
	if record.OSSUploadID != "" && record.Status == "uploading" {
		parts, _ = h.Store.ListParts(record.StorageKey(), record.OSSUploadID)
	}
	if parts == nil {
		parts = []store.PartInfo{}
	}

	status := record.Status
	if status == "uploading" {
		status = "active"
	}

	return c.JSON(fiber.Map{
		"upload_id":     record.UploadID,
		"task_id":       record.TaskID,
		"oss_upload_id": record.OSSUploadID,
		"file_name":     record.Name,
		"file_size":     record.Size,
		"status":        status,
		"parts":         parts,
	})
}
