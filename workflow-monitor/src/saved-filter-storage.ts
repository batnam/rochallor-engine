import { timeRangeError } from "./routes/listFilters";

export type FilterKind = "instances" | "incidents";
export interface SavedFilter {
  id: string;
  name: string;
  search: string;
}
const MAX_FILTERS = 20;
const MAX_STORAGE_BYTES = 64 * 1024;
const fields = {
  instances: [
    "definitionId",
    "definitionVersion",
    "currentStepId",
    "stepStartedBefore",
    "businessKey",
    "status",
    "from",
    "to",
  ],
  incidents: ["definitionId", "jobType", "from", "to"],
};

export function savedFilterKey(kind: FilterKind): string {
  return `rochallor.monitor.savedFilters.${kind}`;
}

export function normalizeFilterSearch(
  kind: FilterKind,
  search: string,
): string {
  if (search.length > 4096)
    throw new Error("Filter URL exceeds 4,096 characters.");
  const source = new URLSearchParams(search);
  source.delete("cursor");
  source.delete("pageSize");
  for (const key of source.keys()) {
    if (
      !fields[kind].includes(key) ||
      (key !== "status" && source.getAll(key).length > 1)
    )
      throw new Error("Unsupported or repeated filter field.");
  }
  const error = timeRangeError(
    source.get("from") ?? "",
    source.get("to") ?? "",
  );
  if (error) throw new Error(error);
  if (source.has("definitionVersion")) {
    const version = source.get("definitionVersion") ?? "";
    if (
      !/^\d+$/.test(version) ||
      Number(version) < 1 ||
      Number(version) > 2147483647 ||
      !source.get("definitionId")
    )
      throw new Error(
        "Definition version requires a Definition ID and a positive integer.",
      );
    source.set("definitionVersion", String(Number(version)));
  }
  if (
    source.has("currentStepId") &&
    (!source.get("currentStepId") || !source.get("definitionId"))
  )
    throw new Error("Current Step ID requires a Definition ID.");
  if (
    source.has("stepStartedBefore") &&
    (!source.get("currentStepId") ||
      !source.get("stepStartedBefore") ||
      timeRangeError(source.get("stepStartedBefore") ?? "", ""))
  )
    throw new Error(
      "Step cutoff requires a Current Step ID and a valid UTC time.",
    );
  const statuses = [...new Set(source.getAll("status"))].sort();
  if (
    statuses.some(
      (status) =>
        !["ACTIVE", "WAITING", "FAILED", "COMPLETED", "CANCELLED"].includes(
          status,
        ),
    )
  )
    throw new Error("Unknown instance status.");
  const result = new URLSearchParams();
  for (const field of fields[kind]) {
    if (field === "status")
      for (const status of statuses) result.append(field, status);
    else if (source.get(field)) result.set(field, source.get(field) ?? "");
  }
  return result.toString();
}

function validateViews(kind: FilterKind, value: unknown): SavedFilter[] {
  if (!Array.isArray(value))
    throw new Error("Invalid saved-filter data. Clear saved filters to reset.");
  if (value.length > MAX_FILTERS)
    throw new Error("Save at most 20 filters per list.");
  const ids = new Set<string>();
  const names = new Set<string>();
  return value.map((item) => {
    if (
      !item ||
      typeof item.id !== "string" ||
      !item.id ||
      item.id.length > 100 ||
      typeof item.name !== "string" ||
      !item.name.trim() ||
      item.name.length > 80 ||
      typeof item.search !== "string" ||
      ids.has(item.id) ||
      names.has(item.name.trim().toLowerCase())
    ) {
      throw new Error(
        "Saved filters need unique names (1–80 characters); at most 20 per list.",
      );
    }
    ids.add(item.id);
    names.add(item.name.trim().toLowerCase());
    return {
      id: item.id,
      name: item.name.trim(),
      search: normalizeFilterSearch(kind, item.search),
    };
  });
}

export function loadSavedFilters(kind: FilterKind): SavedFilter[] {
  const raw = localStorage.getItem(savedFilterKey(kind));
  if (raw === null) return [];
  if (new TextEncoder().encode(raw).length > MAX_STORAGE_BYTES)
    throw new Error(
      "Saved filters exceed the 64 KiB storage limit. Clear saved filters to reset.",
    );
  let parsed: { version?: unknown; views?: unknown };
  try {
    parsed = JSON.parse(raw);
  } catch {
    throw new Error("Invalid saved-filter data. Clear saved filters to reset.");
  }
  if (!parsed || parsed.version !== 1)
    throw new Error(
      "Unsupported saved-filter version. Clear saved filters to reset.",
    );
  return validateViews(kind, parsed.views);
}

export function storeSavedFilters(
  kind: FilterKind,
  views: SavedFilter[],
): SavedFilter[] {
  const valid = validateViews(kind, views);
  const raw = JSON.stringify({ version: 1, views: valid });
  if (new TextEncoder().encode(raw).length > MAX_STORAGE_BYTES)
    throw new Error("Saved filters exceed the 64 KiB storage limit.");
  localStorage.setItem(savedFilterKey(kind), raw);
  return valid;
}
