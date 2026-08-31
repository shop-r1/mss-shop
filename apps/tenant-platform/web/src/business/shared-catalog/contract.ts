import type { SharedCatalogResource } from './catalog';
import type {
  SharedCatalogCapabilities,
  SharedCatalogColumn,
  SharedCatalogDescriptor,
  SharedCatalogListResponse,
  SharedCatalogRecord,
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

function boundedInteger(value: unknown, path: string, minimum: number): number {
  if (!Number.isSafeInteger(value) || (value as number) < minimum) {
    throw new Error(`${path} must be an integer greater than or equal to ${minimum}`);
  }
  return value as number;
}

function parseColumn(value: unknown, index: number): SharedCatalogColumn {
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

function parseCapabilities(value: unknown): SharedCatalogCapabilities {
  if (!isRecord(value)) {
    throw new Error('resource.capabilities must be an object');
  }
  return {
    detail: requiredBoolean(value.detail, 'resource.capabilities.detail'),
    create: requiredBoolean(value.create, 'resource.capabilities.create'),
    update: requiredBoolean(value.update, 'resource.capabilities.update'),
    delete: requiredBoolean(value.delete, 'resource.capabilities.delete'),
  };
}

function parseDescriptor(value: unknown, expected: SharedCatalogResource): SharedCatalogDescriptor {
  if (!isRecord(value)) throw new Error('resource must be an object');
  const name = requiredString(value.name, 'resource.name');
  const domain = requiredString(value.domain, 'resource.domain');
  const titleKey = requiredString(value.titleKey, 'resource.titleKey');
  if (name !== expected.resource || domain !== expected.domain || titleKey !== expected.titleKey) {
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
  const capabilities = parseCapabilities(value.capabilities);
  if (!capabilities.detail) {
    throw new Error(`shared catalog resource ${expected.resource} must support detail`);
  }
  if (!expected.writable && (capabilities.create || capabilities.update || capabilities.delete)) {
    throw new Error(`read-only resource ${expected.resource} advertised write capabilities`);
  }
  return {
    name: expected.resource,
    domain: expected.domain,
    titleKey: expected.titleKey,
    columns,
    capabilities,
  };
}

export function parseSharedCatalogListResponse(
  value: unknown,
  expected: SharedCatalogResource,
): SharedCatalogListResponse {
  if (!isRecord(value)) {
    throw new Error('shared catalogue response must be an object');
  }
  if (!Array.isArray(value.data) || !value.data.every(isRecord)) {
    throw new Error('data must be an array of objects');
  }
  return {
    data: value.data as SharedCatalogRecord[],
    total: boundedInteger(value.total, 'total', 0),
    page: boundedInteger(value.page, 'page', 1),
    pageSize: boundedInteger(value.pageSize, 'pageSize', 1),
    resource: parseDescriptor(value.resource, expected),
  };
}

export function parseSharedCatalogRecordResponse(
  value: unknown,
  expected: SharedCatalogResource,
): SharedCatalogRecord {
  if (!isRecord(value)) {
    throw new Error('shared catalogue record response must be an object');
  }
  if ('data' in value) {
    if (!isRecord(value.data)) throw new Error('data must be an object');
    if ('resource' in value) parseDescriptor(value.resource, expected);
    return value.data;
  }
  return value;
}
