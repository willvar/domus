<script setup lang="ts">
import { useFileSystemStore } from '../../stores/fileSystem'
import ViewerApp from '../ViewerApp.vue'

const fs = useFileSystemStore()
</script>

<template>
  <ViewerApp
    v-for="p in fs.appWindows"
    :key="p.windowId"
    :window-id="p.windowId"
  />
</template>
