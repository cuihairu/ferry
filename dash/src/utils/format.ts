export const GB = 1024 ** 3

/** formatBytes renders human-readable byte sizes; a purely numeric function that does no business wording such as "unlimited". */
export function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`
  const units = ['KiB', 'MiB', 'GiB', 'TiB']
  let v = n
  let i = -1
  do {
    v /= 1024
    i++
  } while (v >= 1024 && i < units.length - 1)
  return `${v.toFixed(v >= 100 ? 0 : 1)} ${units[i]}`
}

/** quotaText renders a quota for display: 0 = unlimited. */
export function quotaText(bytes: number): string {
  return bytes === 0 ? '不限' : formatBytes(bytes)
}

/** formatDate renders local time; empty values yield a dash. */
export function formatDate(iso: string | null | undefined): string {
  if (!iso) return '—'
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return '—'
  const pad = (x: number) => String(x).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`
}
