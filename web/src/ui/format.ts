/**
 * Labels for the metrics list on the connect screen.
 *
 * The byte counters are integers, but the rate is a quotient (bytes over
 * elapsed seconds), so the sub-KB branch has to round: without it the live
 * page printed a rate as "512.5919323304289 B/s" next to values like
 * "5.00 MB".
 */
export function formatBytes(n: number): string {
  if (n >= 1 << 20) return `${(n / (1 << 20)).toFixed(2)} MB`;
  if (n >= 1 << 10) return `${(n / (1 << 10)).toFixed(1)} KB`;
  return `${Math.round(n)} B`;
}
