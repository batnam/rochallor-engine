export class FilterError extends Error {}

export async function fetchList<T>(url: string, label: string): Promise<T> {
  const response = await fetch(url);
  if (response.status === 400) {
    const body = await response.json();
    throw new FilterError(
      typeof body.message === "string"
        ? body.message
        : "Invalid filters. Check the entered values.",
    );
  }
  if (!response.ok)
    throw new Error(
      `Unable to load ${label}. Check the Monitor API connection and try again.`,
    );
  return response.json() as Promise<T>;
}

export function timeRangeError(from: string, to: string): string | null {
  for (const [label, value] of [
    ["From", from],
    ["To", to],
  ]) {
    if (!value) continue;
    if (
      !/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?Z$/.test(value) ||
      Number.isNaN(Date.parse(value)) ||
      new Date(value).toISOString().slice(0, 19) !== value.slice(0, 19)
    ) {
      return `${label} must be a valid UTC date and time.`;
    }
  }
  if (from && to && Date.parse(from) >= Date.parse(to)) {
    return "From must be earlier than To (UTC).";
  }
  return null;
}

export function utcInputValue(value: string): string {
  return value ? `${value.length === 16 ? `${value}:00` : value}Z` : "";
}
