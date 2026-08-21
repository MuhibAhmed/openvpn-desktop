/** Formatting helpers shared by the UI. */

/** bytes renders a byte count the way a person reads it. */
export function bytes(n: number): string {
  if (!n) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const i = Math.min(Math.floor(Math.log(n) / Math.log(1024)), units.length - 1);
  const value = n / Math.pow(1024, i);
  const decimals = value < 10 && i > 0 ? 1 : 0;
  return `${value.toFixed(decimals)} ${units[i]}`;
}

/** rate renders bytes-per-second. */
export function rate(bytesPerSecond: number): string {
  return `${bytes(Math.max(0, bytesPerSecond))}/s`;
}

/**
 * duration renders elapsed seconds as h:mm:ss, dropping the hours until there
 * are some, so a short session does not read as "0:01:12".
 */
export function duration(seconds: number): string {
  if (seconds < 0 || !Number.isFinite(seconds)) return "--";
  const s = Math.floor(seconds % 60);
  const m = Math.floor((seconds / 60) % 60);
  const h = Math.floor(seconds / 3600);
  const pad = (n: number) => String(n).padStart(2, "0");
  return h > 0 ? `${h}:${pad(m)}:${pad(s)}` : `${m}:${pad(s)}`;
}

/** clockTime renders a timestamp as a local wall clock time for the log. */
export function clockTime(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  return d.toLocaleTimeString(undefined, {
    hour12: false,
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  });
}

/** since returns seconds elapsed since an RFC3339 timestamp, or 0. */
export function since(iso: string, now: number): number {
  if (!iso) return 0;
  const t = new Date(iso).getTime();
  if (Number.isNaN(t) || t <= 0) return 0;
  return Math.max(0, (now - t) / 1000);
}

/** isZeroTime reports whether a Go zero time made it across. */
export function isZeroTime(iso: string): boolean {
  if (!iso) return true;
  return iso.startsWith("0001-01-01");
}
