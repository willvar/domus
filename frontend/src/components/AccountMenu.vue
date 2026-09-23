<script setup lang="ts">
import { computed } from 'vue'
import { NAvatar, NButton, NDivider, NSelect, NSwitch, NThing } from 'naive-ui'
import { useI18n } from '../composables/useI18n'
import { usePreferences } from '../composables/usePreferences'
import { useTheme } from '../composables/useTheme'
import { IconAccountCircle, IconArrowLeft, IconContrastCircle, IconDeleteOutline, IconFolderHome, IconViewGridOutline } from '../barrels/icons'

const props = defineProps<{
  avatarUrl?: string
  displayName: string
  username: string
  role?: string
  root: boolean
  showThumbnails: boolean
  /** Renders the place-navigation section (mobile drawer). */
  places?: boolean
  activePlace?: 'files' | 'trash'
}>()

const emit = defineEmits<{
  'update:showThumbnails': [value: boolean]
  admin: []
  logout: []
  navigate: [place: 'files' | 'trash']
}>()

const { t } = useI18n()
const { isDark, toggleDark } = useTheme()
const { prefs, update } = usePreferences()

const qualityChoices = computed(() => [
  { label: t('quality.original'), value: 'original' },
  { label: '2160p', value: '2160p' },
  { label: '1440p', value: '1440p' },
  { label: '1080p', value: '1080p' },
  { label: '720p', value: '720p' },
  { label: '480p', value: '480p' },
])
</script>

<template>
  <div class="account-popover">
    <NThing :title="props.displayName" :description="`@${props.username} · ${props.role || ''}`">
      <template #avatar>
        <NAvatar v-if="props.avatarUrl" round :size="40" :src="props.avatarUrl" />
        <NAvatar v-else round :size="40">{{ props.displayName.slice(0, 1).toUpperCase() }}</NAvatar>
      </template>
    </NThing>

    <template v-if="props.places">
      <NDivider />
      <div class="section-label">{{ t('account.places') }}</div>
      <div class="places-nav">
        <NButton
          quaternary
          block
          size="small"
          :type="props.activePlace === 'files' ? 'primary' : 'default'"
          @click="emit('navigate', 'files')"
        >
          <template #icon><IconFolderHome /></template>
          {{ t('files.my_files') }}
        </NButton>
        <NButton
          quaternary
          block
          size="small"
          :type="props.activePlace === 'trash' ? 'primary' : 'default'"
          @click="emit('navigate', 'trash')"
        >
          <template #icon><IconDeleteOutline /></template>
          {{ t('places.trash') }}
        </NButton>
      </div>
    </template>

    <NDivider />

    <div class="section-label">{{ t('account.preferences') }}</div>
    <div class="preference-group">
      <div class="preference-row">
        <IconContrastCircle width="18" height="18" />
        <span>
          <strong>{{ t('files.dark_mode') }}</strong>
          <small>{{ t('files.dark_mode_hint') }}</small>
        </span>
        <NSwitch
          :value="isDark"
          size="small"
          :aria-label="t('files.dark_mode')"
          @update:value="toggleDark"
        />
      </div>

      <div class="preference-row preference-row--quality">
        <span>
          <strong>{{ t('quality.default') }}</strong>
          <small>{{ t('quality.default_hint') }}</small>
        </span>
        <NSelect
          :value="prefs.playbackQuality"
          size="small"
          :options="qualityChoices"
          :aria-label="t('quality.default')"
          class="quality-select"
          @update:value="value => update({ playbackQuality: String(value) })"
        />
      </div>

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
    </div>

    <div class="account-spacer" />

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
  display: flex;
  width: 300px;
  flex-direction: column;
  padding: 4px 2px 2px;
}

.section-label {
  padding: 0 6px 7px;
  color: var(--domus-subtle);
  font-size: 11px;
  font-weight: 700;
  letter-spacing: .08em;
  text-transform: uppercase;
}

.preference-group { display: grid; gap: 6px; }

/* Drawer layout: pushes the account actions to the bottom edge. */
.account-spacer { min-height: 24px; flex: 1 1 auto; }

.account-popover :deep(.n-thing) { padding: 4px 6px; }

.places-nav { display: grid; gap: 4px; }
.places-nav :deep(.n-button) { justify-content: flex-start; padding-inline: 10px; }
.places-nav :deep(.n-button__content) { justify-content: flex-start; }
.account-popover :deep(.n-thing-main__title) { font-size: 15px; font-weight: 700; }
.account-popover :deep(.n-thing-main__description) { margin-top: 2px; color: var(--domus-muted); font-size: 12px; }

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
  background: var(--domus-surface-2);
}

.preference-row--quality { grid-template-columns: minmax(0, 1fr) auto; }

.quality-select { min-width: 92px; }

.preference-row > svg {
  color: var(--domus-muted);
}

.preference-row strong,
.preference-row small {
  display: block;
}

.preference-row strong { font-size: 13px; }

.preference-row small {
  margin-top: 3px;
  color: var(--domus-muted);
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
