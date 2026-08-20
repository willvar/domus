<script setup lang="ts">
import { NAvatar, NButton, NDivider, NSwitch, NThing } from 'naive-ui'
import { useI18n } from '../composables/useI18n'
import { IconAccountCircle, IconArrowLeft, IconViewGridOutline } from '../barrels/icons'

const props = defineProps<{
  avatarUrl?: string
  displayName: string
  username: string
  role?: string
  root: boolean
  showThumbnails: boolean
}>()

const emit = defineEmits<{
  'update:showThumbnails': [value: boolean]
  admin: []
  logout: []
}>()

const { t } = useI18n()
</script>

<template>
  <div class="account-popover">
    <NThing :title="props.displayName" :description="`@${props.username} · ${props.role || ''}`">
      <template #avatar>
        <NAvatar v-if="props.avatarUrl" round :size="40" :src="props.avatarUrl" />
        <NAvatar v-else round :size="40">{{ props.displayName.slice(0, 1).toUpperCase() }}</NAvatar>
      </template>
    </NThing>

    <NDivider />

    <div class="preference-row">
      <IconViewGridOutline width="18" height="18" />
      <span>
        <strong>{{ t('files.show_thumbnails') }}</strong>
        <small>{{ t('files.show_thumbnails_hint') }}</small>
      </span>
      <NSwitch
        :value="props.showThumbnails"
        size="small"
        :aria-label="t('files.show_thumbnails')"
        @update:value="emit('update:showThumbnails', $event)"
      />
    </div>

    <NDivider />

    <div class="account-actions">
      <NButton v-if="props.root" quaternary block size="small" @click="emit('admin')">
        <template #icon><IconAccountCircle /></template>
        {{ t('titlebar.admin_panel') }}
      </NButton>
      <NButton quaternary block type="error" size="small" @click="emit('logout')">
        <template #icon><IconArrowLeft /></template>
        {{ t('titlebar.sign_out') }}
      </NButton>
    </div>
  </div>
</template>

<style lang="scss" scoped>
.account-popover {
  width: 300px;
  padding: 4px 2px 2px;
}

.account-popover :deep(.n-thing) { padding: 4px 6px; }
.account-popover :deep(.n-thing-main__title) { font-size: 15px; font-weight: 700; }
.account-popover :deep(.n-thing-main__description) { margin-top: 2px; color: #68748a; font-size: 12px; }

.account-popover :deep(.n-divider) {
  margin: 12px 0;
}

.preference-row {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr) auto;
  align-items: center;
  gap: 12px;
  padding: 8px 6px;
  border-radius: 11px;
  background: #f7f8fb;
}

.preference-row > svg {
  color: #626d82;
}

.preference-row strong,
.preference-row small {
  display: block;
}

.preference-row strong { font-size: 13px; }

.preference-row small {
  margin-top: 3px;
  color: #626d82;
  font-size: 11px;
  line-height: 1.35;
}

.account-actions {
  display: grid;
  gap: 4px;
}

.account-actions :deep(.n-button) { padding-inline: 10px; }

.account-actions :deep(.n-button__content) {
  width: 100%;
  justify-content: flex-start;
}
</style>
