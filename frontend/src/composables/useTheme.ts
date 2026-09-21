import { computed, ref } from 'vue'

export type ThemeMode = 'light' | 'dark' | 'system'

const STORAGE_KEY = 'domus_theme_mode'
const media = window.matchMedia('(prefers-color-scheme: dark)')
const systemDark = ref(media.matches)

function storedMode(): ThemeMode {
  const value = localStorage.getItem(STORAGE_KEY)
  return value === 'light' || value === 'dark' || value === 'system' ? value : 'system'
}

const mode = ref<ThemeMode>(storedMode())
const isDark = computed(() => mode.value === 'dark' || (mode.value === 'system' && systemDark.value))

function apply(): void {
  const theme = isDark.value ? 'dark' : 'light'
  document.documentElement.dataset.theme = theme
  document.documentElement.style.colorScheme = theme
}

media.addEventListener('change', (event) => {
  systemDark.value = event.matches
  apply()
})

function setMode(value: ThemeMode): void {
  mode.value = value
  localStorage.setItem(STORAGE_KEY, value)
  apply()
}

export function useTheme() {
  return {
    mode,
    isDark,
    setMode,
    toggleDark(value: boolean) {
      setMode(value ? 'dark' : 'light')
    },
  }
}

apply()
