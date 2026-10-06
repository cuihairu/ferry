export const GB = 1024 ** 3

/** formatBytes 人类可读字节数；纯数值函数，不做「不限」等业务措辞。 */
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

/** quotaText 配额展示：0 = 不限。 */
export function quotaText(bytes: number): string {
  return bytes === 0 ? '不限' : formatBytes(bytes)
}

/** formatDate 本地时间展示，空值给破折号。 */
export function formatDate(iso: string | null | undefined): string {
  if (!iso) return '—'
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return '—'
  const pad = (x: number) => String(x).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`
}
