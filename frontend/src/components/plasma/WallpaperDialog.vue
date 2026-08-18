<script setup lang="ts">
import { ref, computed, onUnmounted } from 'vue'
import { Modal, Button, Select, FormItem } from '../../barrels/breeze'
import { useDevice } from '../../composables/useDevice'
import { usePreferences } from '../../composables/usePreferences'
import { useI18n } from '../../composables/useI18n'
import api from '../../composables/useApi'
import { IconPlus, IconDeleteOutline as IconDelete } from '../../barrels/icons'

const { t } = useI18n()
const { lastPointerInput } = useDevice()
const { prefs, update } = usePreferences()

function mouseTitle(title: string): string | undefined {
  return lastPointerInput.value === 'mouse' ? title : undefined
}

const show = ref(false)
const selectedType = ref('builtin')   // 'builtin' | 'custom'
const selectedBuiltinId = ref(0)
const selectedCustomPath = ref('')
const selectedFit = ref('cover')
const fileInput = ref<HTMLInputElement | null>(null)

// Blob URL cache for custom wallpaper previews
const blobCache = new Map<string, string>() // path -> blobUrl
const customThumbs = ref<Array<{ path: string; name: string; blobUrl: string }>>([]) // [{ path, blobUrl }]

const fitOptions = [
  { label: t('wallpaper.fit_cover'), value: 'cover' },
  { label: t('wallpaper.fit_contain'), value: 'contain' },
  { label: t('wallpaper.fit_fill'), value: 'fill' },
  { label: t('wallpaper.fit_none'), value: 'none' },
]

// Built-in SVG wallpapers as data URIs
const builtins = [
  { id: 0, label: 'Deep Ocean', colors: ['#0d1117', '#151d28', '#0f1923', '#1a3a5c', '#1e4a6e', '#3daee9'] },
]

function builtinSvg(b: { id: number; label: string; colors: string[] }) {
  return `data:image/svg+xml,${encodeURIComponent(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1920 1080">
<defs>
<linearGradient id="bg" x1="0%" y1="0%" x2="100%" y2="100%">
<stop offset="0%" stop-color="${b.colors[0]}"/>
<stop offset="50%" stop-color="${b.colors[1]}"/>
<stop offset="100%" stop-color="${b.colors[2]}"/>
</linearGradient>
<linearGradient id="g1" x1="0%" y1="0%" x2="100%" y2="100%">
<stop offset="0%" stop-color="${b.colors[3]}" stop-opacity="0.6"/>
<stop offset="100%" stop-color="${b.colors[0]}" stop-opacity="0"/>
</linearGradient>
<radialGradient id="s1" cx="25%" cy="35%" r="40%">
<stop offset="0%" stop-color="${b.colors[4]}" stop-opacity="0.35"/>
<stop offset="100%" stop-color="transparent"/>
</radialGradient>
</defs>
<rect width="1920" height="1080" fill="url(#bg)"/>
<rect width="1920" height="1080" fill="url(#s1)"/>
<path d="M0 700 Q480 580 960 650 T1920 600 L1920 1080 L0 1080Z" fill="url(#g1)"/>
<line x1="300" y1="0" x2="900" y2="1080" stroke="${b.colors[5]}" stroke-opacity="0.04" stroke-width="1"/>
<line x1="800" y1="0" x2="1400" y2="1080" stroke="${b.colors[5]}" stroke-opacity="0.03" stroke-width="1"/>
<circle cx="350" cy="300" r="180" fill="none" stroke="${b.colors[5]}" stroke-opacity="0.03" stroke-width="0.5"/>
</svg>`)}`
}

// Preview URL for the currently selected wallpaper
const previewUrl = computed(() => {
  if (selectedType.value === 'builtin') {
    const b = builtins.find(b => b.id === selectedBuiltinId.value) || builtins[0]
    return builtinSvg(b)
  }
  return blobCache.get(selectedCustomPath.value) || ''
})

const previewIsVideo = computed(() => {
  if (selectedType.value !== 'custom') return false
  const ext = selectedCustomPath.value.split('.').pop()?.toLowerCase() || ''
  return ['mp4', 'webm', 'mov'].includes(ext)
})

function open() {
  // Load current settings
  selectedType.value = prefs.wallpaperType || 'builtin'
  selectedBuiltinId.value = prefs.wallpaperBuiltinId ?? 0
  selectedCustomPath.value = prefs.wallpaperPath || ''
  selectedFit.value = prefs.wallpaperFit || 'cover'

  // Load thumbnails for custom wallpapers
  loadCustomThumbs()

  show.value = true
}

async function loadCustomThumbs() {
  const files = prefs.wallpaperFiles || []
  const results: Array<{ path: string; name: string; blobUrl: string }> = []
  for (const name of files) {
    const path = `desktop/${name}`
    if (!blobCache.has(path)) {
      try {
        const { readEncryptedFile } = await import('../../composables/useCryptoUpload')
        const buf = await readEncryptedFile(`/.user/${path}`, true)
        if (buf.byteLength > 0) {
          blobCache.set(path, URL.createObjectURL(new Blob([buf])))
        }
      } catch { /* skip missing files */ continue }
    }
    if (blobCache.has(path)) {
      results.push({ path, name, blobUrl: blobCache.get(path)! })
    }
  }
  customThumbs.value = results
}

function selectBuiltin(id: number) {
  selectedType.value = 'builtin'
  selectedBuiltinId.value = id
}

function selectCustom(path: string) {
  selectedType.value = 'custom'
  selectedCustomPath.value = path
}

function triggerUpload() {
  fileInput.value?.click()
}

async function onFileSelected(e: Event) {
  const file = (e.target as HTMLInputElement).files?.[0]
  if (!file) return
  ;(e.target as HTMLInputElement).value = ''

  const name = `${Date.now()}-${file.name}`
  const path = `desktop/${name}`

  try {
    const { writeEncryptedFile } = await import('../../composables/useCryptoUpload')
    await writeEncryptedFile(`/.user/${path}`, await file.arrayBuffer(), file.type, { internal: true })

    const blobUrl = URL.createObjectURL(file)
    blobCache.set(path, blobUrl)

    // Add to files list
    const files = [...(prefs.wallpaperFiles || []), name]
    update({ wallpaperFiles: files })

    customThumbs.value.push({ path, name, blobUrl })

    // Auto-select the uploaded wallpaper
    selectedType.value = 'custom'
    selectedCustomPath.value = path
  } catch { /* silent */ }
}

async function deleteCustom(thumb: { path: string; name: string; blobUrl: string }) {
  // Remove from files list
  const files = (prefs.wallpaperFiles || []).filter(f => f !== thumb.name)
  update({ wallpaperFiles: files })

  // Remove blob
  if (blobCache.has(thumb.path)) {
    URL.revokeObjectURL(blobCache.get(thumb.path)!)
    blobCache.delete(thumb.path)
  }

  customThumbs.value = customThumbs.value.filter(t => t.path !== thumb.path)

  // If deleted wallpaper was selected, fall back to builtin
  if (selectedCustomPath.value === thumb.path) {
    selectedType.value = 'builtin'
    selectedCustomPath.value = ''
  }
}

function apply() {
  update({
    wallpaperType: selectedType.value,
    wallpaperBuiltinId: selectedBuiltinId.value,
    wallpaperPath: selectedCustomPath.value,
    wallpaperFit: selectedFit.value,
  })
  show.value = false
}

function cancel() {
  show.value = false
}

onUnmounted(() => {
  // Don't revoke — they may be used by Desktop.vue
})

defineExpose({ open })
</script>

<template>
  <Modal :show="show" @close="cancel" @mask-click="cancel">
    <div class="wp-dialog">
      <div class="wp-header">
        <span class="wp-title">{{ t('wallpaper.title') }}</span>
        <button type="button" class="wp-close" @click="cancel">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><line x1="18" y1="6" x2="6" y2="18" /><line x1="6" y1="6" x2="18" y2="18" /></svg>
        </button>
      </div>

      <!-- Preview -->
      <div class="wp-preview" :style="{ background: '#0d1117' }">
        <video
          v-if="previewIsVideo && previewUrl"
          :src="previewUrl"
          :style="{ objectFit: selectedFit } as any"
          class="wp-preview-media"
          autoplay muted loop playsinline
        />
        <img
          v-else-if="previewUrl"
          :src="previewUrl"
          :style="{ objectFit: selectedFit } as any"
          class="wp-preview-media"
        />
      </div>

      <!-- Fit mode -->
      <div class="wp-fit-row">
        <FormItem :label="t('wallpaper.fit')" style="flex:1;margin:0">
          <Select :value="selectedFit" :options="fitOptions" @update:value="v => selectedFit = v" />
        </FormItem>
      </div>

      <!-- Wallpaper grid -->
      <div class="wp-grid">
        <!-- Built-in -->
        <div
          v-for="b in builtins"
          :key="'b-' + b.id"
          class="wp-thumb"
          :class="{ selected: selectedType === 'builtin' && selectedBuiltinId === b.id }"
          @click="selectBuiltin(b.id)"
        >
          <img :src="builtinSvg(b)" class="wp-thumb-img" />
        </div>

        <!-- Upload button -->
        <div class="wp-thumb wp-thumb-upload" @click="triggerUpload">
          <IconPlus width="24" height="24" />
        </div>

        <!-- Custom wallpapers -->
        <div
          v-for="thumb in customThumbs"
          :key="thumb.path"
          class="wp-thumb"
          :class="{ selected: selectedType === 'custom' && selectedCustomPath === thumb.path }"
          @click="selectCustom(thumb.path)"
        >
          <video v-if="thumb.name.match(/\.(mp4|webm|mov)$/i)" :src="thumb.blobUrl" class="wp-thumb-img" muted />
          <img v-else :src="thumb.blobUrl" class="wp-thumb-img" />
          <button class="wp-thumb-delete" :title="mouseTitle(t('wallpaper.delete'))" @click.stop="deleteCustom(thumb)">
            <IconDelete width="14" height="14" />
          </button>
        </div>
      </div>

      <input ref="fileInput" type="file" accept="image/*,video/mp4,video/webm" style="display:none" @change="onFileSelected" />

      <!-- Footer -->
      <div class="wp-footer">
        <Button @click="cancel">{{ t('preview.cancel') }}</Button>
        <Button type="primary" @click="apply">{{ t('wallpaper.apply') }}</Button>
      </div>
    </div>
  </Modal>
</template>

<style lang="scss" scoped>
.wp-dialog {
  width: 560px;
  max-width: 95vw;
  background: var(--breeze-surface);
  border: 1px solid var(--breeze-border);
  border-radius: 6px;
  box-shadow: 0 8px 32px rgba(0, 0, 0, 0.5);
  display: flex;
  flex-direction: column;
}

.wp-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 12px 16px;
  border-bottom: 1px solid var(--breeze-border);
}

.wp-title {
  font-size: 14px;
  font-weight: 500;
  color: var(--breeze-text);
}

.wp-close {
  background: none;
  border: none;
  color: var(--breeze-text-secondary);
  cursor: pointer;
  padding: 4px;
  border-radius: 4px;
  display: flex;

  &:active {
    color: var(--breeze-text);
    background: $hover-white-light;
  }

  @include hover {
    color: var(--breeze-text);
    background: $hover-white-light;
  }
}

.wp-preview {
  margin: 16px 16px 0;
  border-radius: 6px;
  overflow: hidden;
  aspect-ratio: 16 / 9;
  @include flex-center;
  border: 1px solid var(--breeze-border);
}

.wp-preview-media {
  width: 100%;
  height: 100%;
  display: block;
}

.wp-fit-row {
  padding: 12px 16px 0;
}

.wp-grid {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  padding: 12px 16px;
  max-height: 160px;
  overflow-y: auto;
}

.wp-thumb {
  width: 80px;
  height: 50px;
  border-radius: 4px;
  overflow: hidden;
  cursor: pointer;
  border: 2px solid transparent;
  position: relative;
  flex-shrink: 0;
  transition: border-color 0.15s;

  &:active {
    border-color: rgba(255, 255, 255, 0.2);

    .wp-thumb-delete {
      display: flex;
    }
  }

  @include hover {
    border-color: rgba(255, 255, 255, 0.2);

    .wp-thumb-delete {
      display: flex;
    }
  }

  &.selected {
    border-color: var(--breeze-accent);
  }
}

.wp-thumb-img {
  width: 100%;
  height: 100%;
  object-fit: cover;
  display: block;
}

.wp-thumb-upload {
  @include flex-center;
  background: $hover-white-subtle;
  border: 2px dashed var(--breeze-border);
  color: var(--breeze-text-secondary);

  &:active {
    border-color: var(--breeze-accent);
    color: var(--breeze-accent);
  }

  @include hover {
    border-color: var(--breeze-accent);
    color: var(--breeze-accent);
  }
}

.wp-thumb-delete {
  position: absolute;
  top: 2px;
  right: 2px;
  background: rgba(0, 0, 0, 0.6);
  border: none;
  border-radius: 3px;
  color: #fff;
  padding: 2px;
  cursor: pointer;
  display: none;
  line-height: 0;

  &:active {
    background: var(--breeze-danger);
  }

  @include hover {
    background: var(--breeze-danger);
  }
}

.wp-footer {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  padding: 12px 16px;
  border-top: 1px solid var(--breeze-border);
}

@include mobile {
  .wp-grid { max-height: 100px; }
  .wp-footer {
    flex-direction: column;

    :deep(button) { width: 100%; }
  }
}
</style>
