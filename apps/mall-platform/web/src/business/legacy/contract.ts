import type { LegacyResourceEntry } from './catalog';
import type {
  LegacyRecord,
  LegacyResourceCapabilities,
  LegacyResourceColumn,
  LegacyResourceDescriptor,
  LegacyResourceListResponse,
} from './types';

const SAFE_FIELD_NAME = /^[A-Za-z_][A-Za-z0-9_]*$/;

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function requiredString(value: unknown, path: string): string {
  if (typeof value !== 'string' || !value.trim()) {
    throw new Error(`${path} must be a non-empty string`);
  }
  return value.trim();
}

function requiredBoolean(value: unknown, path: string): boolean {
  if (typeof value !== 'boolean') {
    throw new Error(`${path} must be a boolean`);
  }
  return value;
}

function nonNegativeInteger(value: unknown, path: string, minimum = 0): number {
  if (!Number.isInteger(value) || (value as number) < minimum) {
    throw new Error(`${path} must be an integer greater than or equal to ${minimum}`);
  }
  return value as number;
}

function parseColumn(value: unknown, index: number): LegacyResourceColumn {
  if (!isRecord(value)) {
    throw new Error(`resource.columns[${index}] must be an object`);
  }
  const name = requiredString(value.name, `resource.columns[${index}].name`);
  if (!SAFE_FIELD_NAME.test(name)) {
    throw new Error(`resource.columns[${index}].name is not a safe field name`);
  }
  return {
    name,
    label: requiredString(value.label, `resource.columns[${index}].label`),
    type: requiredString(value.type, `resource.columns[${index}].type`).toLowerCase(),
    writable: requiredBoolean(value.writable, `resource.columns[${index}].writable`),
    secret: requiredBoolean(value.secret, `resource.columns[${index}].secret`),
    required: requiredBoolean(value.required, `resource.columns[${index}].required`),
  };
}

function parseCapabilities(value: unknown): LegacyResourceCapabilities {
  if (!isRecord(value)) {
    throw new Error('resource.capabilities must be an object');
  }
  const capabilities = {
    detail: requiredBoolean(value.detail, 'resource.capabilities.detail'),
    create: requiredBoolean(value.create, 'resource.capabilities.create'),
    update: requiredBoolean(value.update, 'resource.capabilities.update'),
    delete: requiredBoolean(value.delete, 'resource.capabilities.delete'),
  };
  if (capabilities.update && !capabilities.detail) {
    throw new Error('resource.capabilities.update requires detail capability');
  }
  return capabilities;
}

function parseDescriptor(value: unknown, expected: LegacyResourceEntry): LegacyResourceDescriptor {
  if (!isRecord(value)) {
    throw new Error('resource must be an object');
  }
  const name = requiredString(value.name, 'resource.name');
  const domain = requiredString(value.domain, 'resource.domain');
  if (name !== expected.resource || domain !== expected.domain) {
    throw new Error(
      `resource identity mismatch: expected ${expected.domain}/${expected.resource}, received ${domain}/${name}`,
    );
  }
  if (!Array.isArray(value.columns)) {
    throw new Error('resource.columns must be an array');
  }
  const columns = value.columns.map(parseColumn);
  const names = new Set<string>();
  for (const column of columns) {
    if (names.has(column.name)) {
      throw new Error(`resource.columns contains duplicate field ${column.name}`);
    }
    names.add(column.name);
  }
  return {
    name: expected.resource,
    domain: expected.domain,
    titleKey: requiredString(value.titleKey, 'resource.titleKey'),
    columns,
    capabilities: parseCapabilities(value.capabilities),
  };
}

export function parseLegacyResourceListResponse(
  value: unknown,
  expected: LegacyResourceEntry,
): LegacyResourceListResponse {
  if (!isRecord(value)) {
    throw new Error('legacy resource response must be an object');
  }
  if (!Array.isArray(value.data) || !value.data.every(isRecord)) {
    throw new Error('data must be an array of objects');
  }
  return {
    data: value.data as LegacyRecord[],
    total: nonNegativeInteger(value.total, 'total'),
    page: nonNegativeInteger(value.page, 'page', 1),
    pageSize: nonNegativeInteger(value.pageSize, 'pageSize', 1),
    resource: parseDescriptor(value.resource, expected),
  };
}

export function parseLegacyRecordResponse(
  value: unknown,
  expected?: LegacyResourceEntry,
): LegacyRecord {
  if (!isRecord(value)) {
    throw new Error('legacy record response must be an object');
  }
  if ('data' in value) {
    if (!isRecord(value.data)) throw new Error('data must be an object');
    if (expected && 'resource' in value) parseDescriptor(value.resource, expected);
    return value.data;
  }
  return value;
}
