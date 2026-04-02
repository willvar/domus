<script setup>
import { watch } from 'vue'
import { useI18n } from '../../../composables/useI18n'
import { usePreferences } from '../../../composables/usePreferences'
import TrayPopup from './TrayPopup.vue'
import BCard from '../../breeze/BCard.vue'
import BFormItem from '../../breeze/BFormItem.vue'
import BInputNumber from '../../breeze/BInputNumber.vue'
import BSwitch from '../../breeze/BSwitch.vue'

const { t } = useI18n()
const { prefs, load, update } = usePreferences()

const props = defineProps({ show: Boolean })
const emit = defineEmits(['update:show'])

watch(() => props.show, (v) => {
  if (v) load()
})
</script>

<template>
  <TrayPopup :show="show" :title="t('account.preferences')" @update:show="v => emit('update:show', v)">
    <div class="prefs-scroll">
      <BCard :title="t('account.large_file_limit')" size="small" style="margin-bottom: 16px">
        <BFormItem :label="t('account.large_file_limit')">
          <BInputNumber
            :value="prefs.largeFileLimitMB"
            :min="1"
            :max="500"
            :step="5"
            @update:value="v => update({ largeFileLimitMB: v })"
          >
            <template #suffix>MB</template>
          </BInputNumber>
        </BFormItem>
      </BCard>

      <BCard :title="t('account.window_title')" size="small">
        <BFormItem :label="t('account.always_center')">
          <BSwitch
            :value="prefs.alwaysCenter"
            @update:value="v => update({ alwaysCenter: v })"
          />
        </BFormItem>
        <template v-if="prefs.alwaysCenter">
          <BFormItem :label="t('account.default_width')">
            <BInputNumber
              :value="prefs.defaultWidth"
              :min="400"
              :max="3840"
              :step="50"
              @update:value="v => update({ defaultWidth: v })"
            >
              <template #suffix>px</template>
            </BInputNumber>
          </BFormItem>
          <BFormItem :label="t('account.default_height')">
            <BInputNumber
              :value="prefs.defaultHeight"
              :min="300"
              :max="2160"
              :step="50"
              @update:value="v => update({ defaultHeight: v })"
            >
              <template #suffix>px</template>
            </BInputNumber>
          </BFormItem>
        </template>
      </BCard>

      <BCard :title="t('prefs.search_title')" size="small" style="margin-top: 16px">
        <BFormItem :label="t('prefs.index_content')">
          <BSwitch
            :value="prefs.indexContent"
            @update:value="v => update({ indexContent: v })"
          />
        </BFormItem>
        <p class="prefs-hint">{{ t('prefs.index_content_hint') }}</p>
      </BCard>

      <BCard :title="t('prefs.session_title')" size="small" style="margin-top: 16px">
        <BFormItem :label="t('prefs.session_isolation')">
          <BSwitch
            :value="prefs.sessionIsolation"
            @update:value="v => update({ sessionIsolation: v })"
          />
        </BFormItem>
        <p class="prefs-hint">{{ t('prefs.session_isolation_hint') }}</p>
      </BCard>
    </div>
  </TrayPopup>
</template>

<style lang="scss" scoped>
.prefs-scroll {
  padding: 12px;

  :deep(.breeze-card) {
    background: rgba(255, 255, 255, 0.02);
    border-color: #3b4045;
  }
}

.prefs-hint {
  font-size: 12px;
  color: var(--breeze-text-disabled);
  margin: 4px 0 0;
}
</style>
