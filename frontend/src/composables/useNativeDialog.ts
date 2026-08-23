import { h, ref } from 'vue'
import type { VNodeChild } from 'vue'
import { NButton, NCheckbox, NInput } from 'naive-ui'
import type { DialogReactive } from 'naive-ui'
import type {
  ConfirmOptions,
  DuplicateDialogOptions,
  DuplicateDialogResult,
  DuplicateDecision,
} from '../types'
import { useI18n } from './useI18n'
import { useAppDialog } from '../ui/feedback'

function linkedContent(content: string): () => VNodeChild {
  return () => {
    const children: VNodeChild[] = []
    const pattern = /https?:\/\/[^\s)]+/g
    let cursor = 0

    for (const match of content.matchAll(pattern)) {
      const index = match.index ?? 0
      if (index > cursor) children.push(content.slice(cursor, index))
      children.push(h('a', {
        href: match[0],
        target: '_blank',
        rel: 'noopener noreferrer',
      }, match[0]))
      cursor = index + match[0].length
    }

    if (cursor < content.length) children.push(content.slice(cursor))
    return h('div', { class: 'domus-dialog-content' }, children)
  }
}

function formatSize(bytes: number | undefined): string {
  if (!bytes) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let value = bytes
  let unit = 0
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024
    unit += 1
  }
  return `${value.toFixed(1)} ${units[unit]}`
}

/** Show a Naive UI prompt and return the entered value, or null when cancelled. */
export function showPrompt(title: string, placeholder: string = ''): Promise<string | null> {
  const { t } = useI18n()
  const dialog = useAppDialog()
  const value = ref('')

  return new Promise((resolve) => {
    let settled = false
    let instance: DialogReactive

    const settle = (result: string | null): void => {
      if (settled) return
      settled = true
      resolve(result)
    }

    const cancel = (): void => settle(null)
    const submit = (): void => settle(value.value || null)

    instance = dialog.create({
      class: 'domus-dialog domus-prompt-dialog',
      title,
      showIcon: false,
      positiveText: t('dialog.ok'),
      negativeText: t('dialog.cancel'),
      content: () => h(NInput, {
        value: value.value,
        placeholder,
        autofocus: true,
        clearable: true,
        'onUpdate:value': (next: string) => { value.value = next },
        onKeydown: (event: KeyboardEvent) => {
          if (event.key !== 'Enter' || event.isComposing) return
          event.preventDefault()
          submit()
          instance.destroy()
        },
      }),
      onPositiveClick: submit,
      onNegativeClick: cancel,
      onClose: cancel,
      onMaskClick: cancel,
      onEsc: cancel,
    })
  })
}

/** Show a Naive UI confirmation dialog. */
export function showConfirm(
  title: string,
  content: string = '',
  options: ConfirmOptions = {},
): Promise<boolean> {
  const { t } = useI18n()
  const dialog = useAppDialog()

  return new Promise((resolve) => {
    let settled = false
    const settle = (result: boolean): void => {
      if (settled) return
      settled = true
      resolve(result)
    }

    const create = options.icon === 'warning' ? dialog.warning : dialog.create
    create({
      class: 'domus-dialog domus-confirm-dialog',
      title,
      content: content ? linkedContent(content) : undefined,
      showIcon: options.icon === 'warning',
      positiveText: options.positiveText || t('dialog.ok'),
      negativeText: t('dialog.cancel'),
      positiveButtonProps: {
        type: options.positiveType === 'error' ? 'error' : 'primary',
      },
      onPositiveClick: () => settle(true),
      onNegativeClick: () => settle(false),
      onClose: () => settle(false),
      onMaskClick: () => settle(false),
      onEsc: () => settle(false),
    })
  })
}

/** Show the upload conflict choices with Naive UI controls. */
export function showDuplicateDialog(
  options: DuplicateDialogOptions,
): Promise<DuplicateDialogResult | null> {
  const { t } = useI18n()
  const dialog = useAppDialog()
  const applyToAll = ref(false)

  return new Promise((resolve) => {
    let settled = false
    let instance: DialogReactive

    const settle = (result: DuplicateDialogResult | null): void => {
      if (settled) return
      settled = true
      resolve(result)
    }

    const choose = (action: DuplicateDecision['action']): void => {
      settle({ action, applyToAll: applyToAll.value })
      instance.destroy()
    }

    instance = dialog.create({
      class: 'domus-dialog domus-duplicate-dialog',
      title: options.title,
      showIcon: false,
      style: { width: 'min(520px, calc(100vw - 32px))' },
      content: () => h('div', { class: 'duplicate-dialog' }, [
        h('p', { class: 'duplicate-dialog__summary' },
          t('upload.duplicate_summary', { name: options.incomingName })),
        h('div', { class: 'duplicate-dialog__grid' }, [
          h('div', { class: 'duplicate-dialog__item' }, [
            h('span', { class: 'duplicate-dialog__label' }, t('upload.duplicate_existing')),
            h('strong', { class: 'duplicate-dialog__name' }, options.existingName),
            h('span', { class: 'duplicate-dialog__meta' },
              options.existingIsDir ? t('upload.duplicate_folder') : formatSize(options.existingSize)),
          ]),
          h('div', { class: 'duplicate-dialog__item' }, [
            h('span', { class: 'duplicate-dialog__label' }, t('upload.duplicate_incoming')),
            h('strong', { class: 'duplicate-dialog__name' }, options.incomingName),
            h('span', { class: 'duplicate-dialog__meta' }, formatSize(options.incomingSize)),
          ]),
        ]),
        h(NCheckbox, {
          checked: applyToAll.value,
          class: 'duplicate-dialog__apply-all',
          'onUpdate:checked': (checked: boolean) => { applyToAll.value = checked },
        }, { default: () => t('upload.duplicate_apply_all') }),
      ]),
      action: () => h('div', { class: 'duplicate-dialog__actions' }, [
        h(NButton, { onClick: () => { settle(null); instance.destroy() } },
          { default: () => t('upload.duplicate_cancel') }),
        h(NButton, { onClick: () => choose('skip') },
          { default: () => t('upload.duplicate_skip') }),
        h(NButton, { onClick: () => choose('rename') },
          { default: () => t('upload.duplicate_rename') }),
        ...(options.allowMerge
          ? [h(NButton, { type: 'primary', onClick: () => choose('merge') },
              { default: () => t('upload.duplicate_merge') })]
          : []),
        h(NButton, { type: 'primary', onClick: () => choose('replace') },
          { default: () => t('upload.duplicate_replace') }),
      ]),
      onClose: () => settle(null),
      onMaskClick: () => settle(null),
      onEsc: () => settle(null),
    })
  })
}
