import { ref } from 'vue'
import type { Ref } from 'vue'
import type { DialogState, ConfirmOptions, DuplicateDialogOptions, DuplicateDialogResult } from '../types'

// Global dialog state — driven by a component in App.vue
const dialogState: Ref<DialogState | null> = ref(null)

/**
 * Show a prompt dialog (input). Returns the entered string or null if cancelled.
 */
export function showPrompt(title: string, placeholder: string = ''): Promise<string | null> {
  return new Promise((resolve) => {
    dialogState.value = {
      type: 'prompt',
      title,
      content: '',
      placeholder,
      resolve: resolve as (value: unknown) => void,
    }
  })
}

/**
 * Show a confirm dialog. Returns true/false.
 */
export function showConfirm(title: string, content: string = '', options: ConfirmOptions = {}): Promise<boolean> {
  return new Promise((resolve) => {
    dialogState.value = {
      type: 'confirm',
      title,
      content,
      icon: options.icon || '',
      positiveText: options.positiveText || '',
      positiveType: options.positiveType || '',
      resolve: resolve as (value: unknown) => void,
    }
  })
}

/**
 * Show an alert dialog. Returns when dismissed.
 */
export function showAlert(title: string, content: string = ''): Promise<void> {
  return new Promise((resolve) => {
    dialogState.value = {
      type: 'alert',
      title,
      content,
      resolve: resolve as (value: unknown) => void,
    }
  })
}

/**
 * Show a duplicate conflict dialog. Returns { action, applyToAll } or null if cancelled.
 */
export function showDuplicateDialog(options: DuplicateDialogOptions): Promise<DuplicateDialogResult | null> {
  return new Promise((resolve) => {
    dialogState.value = {
      type: 'duplicate',
      title: options.title,
      incomingName: options.incomingName,
      incomingSize: options.incomingSize,
      existingName: options.existingName,
      existingSize: options.existingSize,
      existingIsDir: options.existingIsDir,
      resolve: resolve as (value: unknown) => void,
    }
  })
}

export function useDialogState(): Ref<DialogState | null> {
  return dialogState
}
