<script setup lang="ts">
import { onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { Spin } from '../barrels/breeze'
import { useDevice } from '../composables/useDevice'

const router = useRouter()
const { isMobile } = useDevice()

onMounted(() => {
  router.replace(isMobile.value ? '/m/files' : '/desktop')
})
</script>

<template>
  <div class="platform-entry">
    <Spin size="large" />
  </div>
</template>

<style lang="scss" scoped>
.platform-entry {
  @include flex-center;
  min-height: 100dvh;
  background: var(--breeze-bg);
}
</style>
