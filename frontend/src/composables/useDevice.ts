import { computed, onMounted, onUnmounted, ref } from 'vue'
import type { ComputedRef, Ref } from 'vue'

const width: Ref<number> = ref(window.innerWidth)
const height: Ref<number> = ref(window.innerHeight)
const reducedMotionQuery = window.matchMedia('(prefers-reduced-motion: reduce)')
const prefersReducedMotion: Ref<boolean> = ref(reducedMotionQuery.matches)

type PointerInput = 'mouse' | 'touch' | 'pen'
const lastPointerInput: Ref<PointerInput> = ref(
  'ontouchstart' in window || navigator.maxTouchPoints > 0 ? 'touch' : 'mouse'
)

let listeners = 0
let pointerListenerAttached = false

function updateViewport(): void {
  width.value = window.innerWidth
  height.value = window.innerHeight
}

function handleReducedMotionChange(e: MediaQueryListEvent): void {
  prefersReducedMotion.value = e.matches
}

function handlePointerDown(e: PointerEvent): void {
  if (e.pointerType === 'touch' || e.pointerType === 'pen') {
    lastPointerInput.value = e.pointerType
  } else {
    lastPointerInput.value = 'mouse'
  }
}

function attach(): void {
  if (listeners++ > 0) return
  window.addEventListener('resize', updateViewport)
  if ('visualViewport' in window && window.visualViewport) {
    window.visualViewport.addEventListener('resize', updateViewport)
  }
  reducedMotionQuery.addEventListener('change', handleReducedMotionChange)
  if (!pointerListenerAttached) {
    pointerListenerAttached = true
    document.addEventListener('pointerdown', handlePointerDown, { passive: true })
  }
}

function detach(): void {
  listeners = Math.max(0, listeners - 1)
  if (listeners > 0) return
  window.removeEventListener('resize', updateViewport)
  if ('visualViewport' in window && window.visualViewport) {
    window.visualViewport.removeEventListener('resize', updateViewport)
  }
  reducedMotionQuery.removeEventListener('change', handleReducedMotionChange)
}

export function useDevice(): {
  width: Ref<number>
  height: Ref<number>
  isMobile: ComputedRef<boolean>
  isTablet: ComputedRef<boolean>
  hasTouch: ComputedRef<boolean>
  prefersReducedMotion: Ref<boolean>
  lastPointerInput: Ref<PointerInput>
  isTouchInput: ComputedRef<boolean>
  safeAreaBottom: ComputedRef<string>
  safeAreaTop: ComputedRef<string>
} {
  onMounted(attach)
  onUnmounted(detach)

  const isMobile = computed(() => width.value < 768)
  const isTablet = computed(() => width.value >= 768 && width.value < 1100)
  const hasTouch = computed(() => 'ontouchstart' in window || navigator.maxTouchPoints > 0)
  const isTouchInput = computed(() => lastPointerInput.value === 'touch')
  const safeAreaBottom = computed(() => 'env(safe-area-inset-bottom)')
  const safeAreaTop = computed(() => 'env(safe-area-inset-top)')

  return {
    width,
    height,
    isMobile,
    isTablet,
    hasTouch,
    prefersReducedMotion,
    lastPointerInput,
    isTouchInput,
    safeAreaBottom,
    safeAreaTop,
  }
}
