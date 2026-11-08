package dofsbridge

import (
	"context"

	"github.com/willvar/dofs"

	"domus/internal/notify"
)

// Both metadata drivers already implement this atomic-write contract. Notify
// only after a successful return; no database-specific triggers or file watches.
type notifyingMetadata struct{ dofs.MetadataStore }

func metadataChanged(namespace string, err error) {
	if err == nil {
		notify.Publish(notify.Event{Kind: "dofs", UserID: namespace})
	}
}

func (m *notifyingMetadata) CreateNode(ctx context.Context, request dofs.CreateNodeRequest) (dofs.Node, error) {
	node, err := m.MetadataStore.CreateNode(ctx, request)
	metadataChanged(request.NamespaceID, err)
	return node, err
}

func (m *notifyingMetadata) CommitGeneration(ctx context.Context, request dofs.CommitGenerationRequest) (dofs.Node, bool, error) {
	node, applied, err := m.MetadataStore.CommitGeneration(ctx, request)
	if applied {
		metadataChanged(request.NamespaceID, err)
	}
	return node, applied, err
}

func (m *notifyingMetadata) CommitExternalUpload(ctx context.Context, namespace, id string) (dofs.Node, bool, error) {
	node, applied, err := m.MetadataStore.CommitExternalUpload(ctx, namespace, id)
	if applied {
		metadataChanged(namespace, err)
	}
	return node, applied, err
}

func (m *notifyingMetadata) Rename(ctx context.Context, request dofs.RenameRequest) (dofs.Node, *dofs.Node, error) {
	node, replaced, err := m.MetadataStore.Rename(ctx, request)
	metadataChanged(request.NamespaceID, err)
	return node, replaced, err
}

func (m *notifyingMetadata) Remove(ctx context.Context, request dofs.RemoveRequest) (dofs.Node, error) {
	node, err := m.MetadataStore.Remove(ctx, request)
	metadataChanged(request.NamespaceID, err)
	return node, err
}

func (m *notifyingMetadata) Chmod(ctx context.Context, namespace string, inode uint64, mode uint32) (dofs.Node, error) {
	node, err := m.MetadataStore.Chmod(ctx, namespace, inode, mode)
	metadataChanged(namespace, err)
	return node, err
}
