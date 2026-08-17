// Reads the Domus key first, then copies a pre-rename value forward without
// deleting it. Keeping the legacy value makes a rollback non-destructive.
export function readMigratedStorage(primaryKey: string, legacyKey: string): string | null {
  const current = localStorage.getItem(primaryKey)
  if (current !== null) return current
  const legacy = localStorage.getItem(legacyKey)
  if (legacy !== null) localStorage.setItem(primaryKey, legacy)
  return legacy
}
