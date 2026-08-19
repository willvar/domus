<script setup lang="ts">
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { IconChevronLeft } from '../../barrels/icons'
import MobileTopBar from '../../components/mobile/MobileTopBar.vue'
import { useI18n } from '../../composables/useI18n'
import { useFileSystemStore } from '../../stores/fileSystem'

const route = useRoute()
const router = useRouter()
const fs = useFileSystemStore()
const { t } = useI18n()
const entries = computed(() => [{ name: t('mobile.path.home'), path: '/' }, ...fs.pathSegments])

function jump(path: string): void {
  router.push({ path: '/m/files', query: { path } })
}

function handleBack(): void {
  const from = typeof route.query.from === 'string' ? route.query.from : ''
  if (from) {
    router.push(from)
    return
  }
  router.back()
}
</script>

<template>
  <section class="mobile-page">
    <MobileTopBar :title="t('mobile.path.title')" :subtitle="t('mobile.path.subtitle')" show-back @back="handleBack">
      <template #back><IconChevronLeft width="18" height="18" /></template>
    </MobileTopBar>
    <div class="mobile-page-body">
      <div class="path-list">
        <button v-for="entry in entries" :key="entry.path" class="path-item" @click="jump(entry.path)">
          {{ entry.name }}
          <span>{{ entry.path }}</span>
        </button>
      </div>
    </div>
  </section>
</template>

<style lang="scss" scoped>
.mobile-page {
  min-height: 100dvh;
}

.mobile-page-body {
  padding: 16px 16px 188px;
}

.path-list {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.path-item {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 4px;
  min-height: 68px;
  border: none;
  border-radius: 20px;
  background: rgba(255, 255, 255, 0.94);
  box-shadow: 0 12px 28px rgba(11, 24, 36, 0.06);
  padding: 14px 16px;
  color: #102030;
  font-size: 15px;
  font-weight: 700;
  transition: transform 0.16s ease, box-shadow 0.16s ease, background 0.16s ease;

  &:active {
    transform: scale(0.988);
    box-shadow: 0 8px 18px rgba(11, 24, 36, 0.09);
  }

  span {
    font-size: 12px;
    font-weight: 500;
    color: rgba(16, 32, 48, 0.56);
  }
}
</style>
