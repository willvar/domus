import { ref, computed } from 'vue'
import type { Ref, ComputedRef } from 'vue'
import type { AxiosError } from 'axios'
import type { Locale, Messages } from '../types'
import zh from '../i18n/zh'
import en from '../i18n/en'
import { readMigratedStorage } from '../utils/storageCompat'

const messages: Messages = { zh, en }

const savedLocale: string | null = readMigratedStorage('domus_locale', 'zephyr_locale')
const browserLang: Locale = navigator.language?.startsWith('zh') ? 'zh' : 'en'
const locale: Ref<Locale> = ref((savedLocale as Locale) || browserLang)

export function useI18n(): {
  t: (key: string, params?: Record<string, string | number>) => string
  te: (error: AxiosError<{ error?: string }> | null | undefined, fallbackKey?: string) => string
  locale: ComputedRef<Locale>
  setLocale: (lang: Locale) => void
} {
  function t(key: string, params: Record<string, string | number> = {}): string {
    let text: string = messages[locale.value]?.[key] || messages.en[key] || key
    for (const [k, v] of Object.entries(params)) {
      text = text.replace(`{${k}}`, String(v))
    }
    return text
  }

  // Translate an API error response to localized text.
  // Backend returns snake_case error keys, frontend looks up "error." + key.
  function te(error: AxiosError<{ error?: string }> | null | undefined, fallbackKey?: string): string {
    const key: string | undefined = (error?.response?.data as { error?: string } | undefined)?.error
    if (key) {
      const i18nKey: string = 'error.' + key
      const translated: string | undefined = messages[locale.value]?.[i18nKey] || messages.en?.[i18nKey]
      if (translated) return translated
      if (import.meta.env.DEV) {
        console.warn(`[i18n] missing translation for "${i18nKey}"`)
      }
      return key.replace(/_/g, ' ').replace(/^./, (c: string) => c.toUpperCase())
    }
    return fallbackKey ? t(fallbackKey) : (error?.message || '')
  }

  function setLocale(lang: Locale): void {
    locale.value = lang
    localStorage.setItem('domus_locale', lang)
  }

  const currentLocale: ComputedRef<Locale> = computed(() => locale.value)

  return { t, te, locale: currentLocale, setLocale }
}
