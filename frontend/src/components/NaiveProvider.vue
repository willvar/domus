<script setup lang="ts">
import { computed } from 'vue'
import {
  NConfigProvider,
  NDialogProvider,
  NGlobalStyle,
  NLoadingBarProvider,
  NMessageProvider,
  NNotificationProvider,
  dateEnUS,
  dateZhCN,
  enUS,
  zhCN,
} from 'naive-ui'
import { useI18n } from '../composables/useI18n'
import { domusThemeOverrides } from '../ui/theme'
import NaiveFeedbackBridge from './NaiveFeedbackBridge.vue'

const { locale } = useI18n()
const naiveLocale = computed(() => (locale.value === 'zh' ? zhCN : enUS))
const naiveDateLocale = computed(() => (locale.value === 'zh' ? dateZhCN : dateEnUS))
</script>

<template>
  <NConfigProvider
    :theme-overrides="domusThemeOverrides"
    :locale="naiveLocale"
    :date-locale="naiveDateLocale"
  >
    <NGlobalStyle />
    <NLoadingBarProvider>
      <NDialogProvider>
        <NNotificationProvider placement="bottom-right" :max="4">
          <NMessageProvider placement="top-right" :max="4">
            <NaiveFeedbackBridge />
            <slot />
          </NMessageProvider>
        </NNotificationProvider>
      </NDialogProvider>
    </NLoadingBarProvider>
  </NConfigProvider>
</template>
