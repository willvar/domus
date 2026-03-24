import { ref } from 'vue'

// Global dialog state — driven by a component in App.vue
const dialogState = ref(null)

/**
 * Show a prompt dialog (input). Returns the entered string or null if cancelled.
 */
export function showPrompt(title, placeholder = '') {
  return new Promise((resolve) => {
    dialogState.value = {
      type: 'prompt',
      title,
      content: '',
      placeholder,
      resolve,
    }
  })
}

/**
 * Show a confirm dialog. Returns true/false.
 */
export function showConfirm(title, content = '') {
  return new Promise((resolve) => {
    dialogState.value = {
      type: 'confirm',
      title,
      content,
      resolve,
    }
  })
}

/**
 * Show an alert dialog. Returns when dismissed.
 */
export function showAlert(title, content = '') {
  return new Promise((resolve) => {
    dialogState.value = {
      type: 'alert',
      title,
      content,
      resolve,
    }
  })
}

/**
 * Show a duplicate conflict dialog. Returns { action, applyToAll } or null if cancelled.
 */
export function showDuplicateDialog(options) {
  return new Promise((resolve) => {
    dialogState.value = {
      type: 'duplicate',
      title: options.title,
      incomingName: options.incomingName,
      incomingSize: options.incomingSize,
      existingName: options.existingName,
      existingSize: options.existingSize,
      existingIsDir: options.existingIsDir,
      resolve,
    }
  })
}

export function useDialogState() {
  return dialogState
}
