/// <reference types="vite/client" />

declare module '*.vue' {
  import type { DefineComponent } from 'vue'
  const component: DefineComponent<Record<string, unknown>, Record<string, unknown>, unknown>
  export default component
}

declare module '~icons/*' {
  import type { FunctionalComponent, SVGAttributes } from 'vue'
  const component: FunctionalComponent<SVGAttributes>
  export default component
}

declare module 'papaparse' {
  export function parse(input: string, config?: Record<string, unknown>): {
    data: Record<string, string>[] | string[][]
    meta: { fields?: string[]; [key: string]: unknown }
  }
}
