import { defineStore } from 'pinia'
import { ref } from 'vue'
import type { Ref } from 'vue'

export const useActivityStore = defineStore('activity', () => {
  const open: Ref<boolean> = ref(false)

  function toggle(): void {
    open.value = !open.value
  }

  function close(): void {
    open.value = false
  }

  function show(): void {
    open.value = true
  }

  return {
    open,
    toggle,
    close,
    show,
  }
})
