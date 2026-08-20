<script setup lang="ts">
import { onUnmounted } from 'vue'
import {
  useDialog,
  useLoadingBar,
  useMessage,
  useNotification,
} from 'naive-ui'
import { installFeedbackApis, uninstallFeedbackApis } from '../ui/feedback'

const apis = {
  dialog: useDialog(),
  loadingBar: useLoadingBar(),
  message: useMessage(),
  notification: useNotification(),
}

installFeedbackApis(apis)
onUnmounted(() => uninstallFeedbackApis(apis))
</script>

<template></template>
