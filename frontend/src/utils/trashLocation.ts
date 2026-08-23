export const TRASH_ROOT_LOCATION = 'trash:///'

export interface TrashLocation {
  id: string | null
  relativePath: string
}

export type TrashRouteQuery = Record<string, string>

export function isTrashLocation(value: string): boolean {
  return value === TRASH_ROOT_LOCATION || value.startsWith(TRASH_ROOT_LOCATION)
}

export function parseTrashLocation(value: string): TrashLocation | null {
  if (!isTrashLocation(value)) return null
  const remainder = value.slice(TRASH_ROOT_LOCATION.length)
  if (!remainder) return { id: null, relativePath: '/' }
  const slash = remainder.indexOf('/')
  try {
    if (slash < 0) return { id: decodeURIComponent(remainder), relativePath: '/' }
    const id = decodeURIComponent(remainder.slice(0, slash))
    const tail = remainder.slice(slash + 1)
    return { id, relativePath: tail ? '/' + tail : '/' }
  } catch {
    return null
  }
}

export function trashItemLocation(id: string, relativePath: string = '/', isDir: boolean = false): string {
  const root = TRASH_ROOT_LOCATION + encodeURIComponent(id)
  if (!relativePath || relativePath === '/') return root + (isDir ? '/' : '')
  const relative = relativePath.startsWith('/') ? relativePath : '/' + relativePath
  return root + relative + (isDir && !relative.endsWith('/') ? '/' : '')
}

export function normalizeTrashDirectoryLocation(value: string): string {
  if (!isTrashLocation(value)) return value
  return value.endsWith('/') ? value : value + '/'
}

export function trashParentLocation(value: string): string {
  const location = parseTrashLocation(value)
  if (!location?.id) return TRASH_ROOT_LOCATION
  if (location.relativePath === '/') return TRASH_ROOT_LOCATION
  const trimmed = location.relativePath.replace(/\/$/, '')
  const separator = trimmed.lastIndexOf('/')
  if (separator <= 0) return trashItemLocation(location.id, '/', true)
  return trashItemLocation(location.id, trimmed.slice(0, separator + 1), true)
}

/** Encode the virtual trash location without overloading the real file path query. */
export function trashLocationRouteQuery(value: string): TrashRouteQuery {
  const location = parseTrashLocation(value)
  if (!location?.id) return { place: 'trash' }
  const query: TrashRouteQuery = { place: 'trash', trash: location.id }
  if (location.relativePath !== '/') query.path = location.relativePath
  return query
}

/** Decode a trash location from the public /files route query. */
export function trashLocationFromRouteQuery(
  place: unknown,
  trash: unknown,
  relativePath: unknown,
): string | null {
  if (place !== 'trash') return null
  if (typeof trash !== 'string' || !trash) return TRASH_ROOT_LOCATION
  const relative = typeof relativePath === 'string' && relativePath.startsWith('/')
    ? relativePath
    : '/'
  return trashItemLocation(trash, relative, true)
}
