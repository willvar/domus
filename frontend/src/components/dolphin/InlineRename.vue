<script setup>
import { ref, onMounted, nextTick } from 'vue'
import { useFileSystemStore } from '../../stores/fileSystem'
import { useI18n } from '../../composables/useI18n'
import { useMessage } from '../../composables/useMessage'
import BInput from '../breeze/BInput.vue'

const props = defineProps({
  file: { type: Object, required: true },
})

const fs = useFileSystemStore()
const message = useMessage()
const { t, te } = useI18n()
const inputRef = ref(null)

// Strip trailing / for dirs and get just the name
const newName = ref(props.file.name)
let committed = false

onMounted(() => {
  nextTick(() => {
    inputRef.value?.focus()
    // Select name without extension for files
    if (!props.file.is_dir) {
      const dotIndex = newName.value.lastIndexOf('.')
      if (dotIndex > 0) {
        inputRef.value?.select(0, dotIndex)
      }
    }
  })
})

async function commit() {
  if (committed) return
  committed = true

  const name = newName.value.trim()
  if (!name || name === props.file.name) {
    fs.cancelRename()
    return
  }

  try {
    await fs.rename(props.file.path, name, props.file.is_dir)
  } catch (e) {
    committed = false
    message.error(t('dialog.rename_failed') + ': ' + te(e))
  }
}
</script>

<template>
  <BInput
    ref="inputRef"
    :value="newName"
    size="tiny"
    class="rename-input"
    @update:value="v => newName = v"
    @keyup.enter="commit"
    @keyup.escape="fs.cancelRename()"
    @blur="commit"
    @click.stop
  />
</template>

<style scoped>
.rename-input {
  width: 100%;
  min-width: 60px;
}
</style>
