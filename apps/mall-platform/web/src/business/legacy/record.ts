import type { LegacyRecord, LegacyResourceColumn } from './types';

const JSON_TYPES = new Set(['json', 'jsonb']);

export function isJSONColumn(column: LegacyResourceColumn): boolean {
  return JSON_TYPES.has(column.type.toLowerCase());
}

export function visibleLegacyColumns(
  columns: readonly LegacyResourceColumn[],
): LegacyResourceColumn[] {
  return columns.filter((column) => !column.secret);
}

export function writableLegacyColumns(
  columns: readonly LegacyResourceColumn[],
): LegacyResourceColumn[] {
  return columns.filter((column) => column.writable);
}

export function legacyRecordID(record: LegacyRecord): string | undefined {
  for (const key of ['id', 'ID', '_id', 'key']) {
    const value = record[key];
    if ((typeof value === 'string' || typeof value === 'number') && String(value).trim()) {
      return String(value);
    }
  }
  return undefined;
}

export function toLegacyFormValues(
  columns: readonly LegacyResourceColumn[],
  record: LegacyRecord,
): LegacyRecord {
  const result: LegacyRecord = {};
  for (const column of writableLegacyColumns(columns)) {
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

export function toLegacyMutationPayload(
  columns: readonly LegacyResourceColumn[],
  values: LegacyRecord,
): LegacyRecord {
  const result: LegacyRecord = {};
  for (const column of writableLegacyColumns(columns)) {
    const value = values[column.name];
    if (column.secret && (value === undefined || value === null || value === '')) {
      continue;
    }
    if (value === undefined) {
      continue;
    }
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
