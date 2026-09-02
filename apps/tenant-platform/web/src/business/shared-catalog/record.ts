import type { SharedCatalogColumn, SharedCatalogRecord } from './types';

const JSON_TYPES = new Set(['json', 'jsonb']);

export function isJSONColumn(column: SharedCatalogColumn): boolean {
  return JSON_TYPES.has(column.type.toLowerCase());
}

export function visibleColumns(columns: readonly SharedCatalogColumn[]): SharedCatalogColumn[] {
  return columns.filter((column) => !column.secret);
}

export function writableColumns(columns: readonly SharedCatalogColumn[]): SharedCatalogColumn[] {
  return columns.filter((column) => column.writable);
}

export function recordID(record: SharedCatalogRecord): string | undefined {
  for (const key of ['id', 'ID', '_id', 'key']) {
    const value = record[key];
    if ((typeof value === 'string' || typeof value === 'number') && String(value).trim()) {
      return String(value);
    }
  }
  return undefined;
}

export function toFormValues(
  columns: readonly SharedCatalogColumn[],
  record: SharedCatalogRecord,
): SharedCatalogRecord {
  const result: SharedCatalogRecord = {};
  for (const column of writableColumns(columns)) {
    if (column.secret) {
      result[column.name] = undefined;
      continue;
    }
    const value = record[column.name];
    if (isJSONColumn(column) && value !== undefined && value !== null) {
      result[column.name] = typeof value === 'string' ? value : JSON.stringify(value, null, 2);
      continue;
    }
    result[column.name] = value;
  }
  return result;
}

export function toMutationPayload(
  columns: readonly SharedCatalogColumn[],
  values: SharedCatalogRecord,
): SharedCatalogRecord {
  const result: SharedCatalogRecord = {};
  for (const column of writableColumns(columns)) {
    const value = values[column.name];
    if (
      column.secret &&
      (value === undefined || value === null || (typeof value === 'string' && !value.trim()))
    ) {
      continue;
    }
    if (value === undefined) continue;
    if (isJSONColumn(column) && typeof value === 'string') {
      result[column.name] = value.trim() ? JSON.parse(value) : null;
      continue;
    }
    result[column.name] = value;
  }
  return result;
}

export function isValidJSONText(value: unknown): boolean {
  if (value === undefined || value === null || value === '') return true;
  if (typeof value !== 'string') return false;
  try {
    JSON.parse(value);
    return true;
  } catch {
    return false;
  }
}
