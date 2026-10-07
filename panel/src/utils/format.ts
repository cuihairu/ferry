export const GB = 1024 ** 3

// formatBytes 以 GB/PB 口径展示字节数（小内存 VPS 上配额以 GiB 计）。
export function formatBytes(n: number): string {
  if (n >= 1024 ** 5) return `${(n / 1024 ** 5).toFixed(2)} PB`
  if (n >= GB) return `${(n / GB).toFixed(n % GB === 0 ? 0 : 1)} GB`
  if (n >= 1024 ** 2) return `${(n / 1024 ** 2).toFixed(0)} MB`
  return `${n} B`
}

export function formatDate(v: string | null): string {
  if (!v) return '—'
  const d = new Date(v)
  if (Number.isNaN(d.getTime())) return '—'
  const p = (x: number) => String(x).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`
}
