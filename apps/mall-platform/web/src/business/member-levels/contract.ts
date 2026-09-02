import { type CurrentUser, hasPermission } from '@mss-boot-io/admin-web/runtime';
import { memberLevelsPermissionPaths } from './paths';
import type {
  CreateMemberLevelInput,
  MemberLevel,
  MemberLevelCapabilities,
  MemberLevelEditorValues,
  MemberLevelFilterValues,
  MemberLevelOperations,
  MemberLevelPage,
  MemberLevelQuery,
  MemberLevelRevisionInput,
  MemberLevelStatus,
  MemberLevelWritableStatus,
  UpdateMemberLevelInput,
} from './types';

const memberLevelKeys = [
  'id',
  'name',
  'discountPercent',
  'status',
  'isDefault',
  'createdAt',
  'updatedAt',
  'revision',
] as const;

const memberLevelPageKeys = [
  'data',
  'total',
  'current',
  'pageSize',
  'integrity',
  'operations',
] as const;
const integrityKeys = [
  'flaggedDefaultCount',
  'enabledDefaultCount',
  'invalidDefaultCount',
] as const;
const operationKeys = ['create', 'update', 'setDefault', 'delete'] as const;
const writableStatuses = ['enabled', 'disabled'] as const;
const responseStatuses = [...writableStatuses, 'unknown'] as const;

export const memberLevelNameMaxBytes = 100;
export const memberLevelPageSizes = [20, 50, 100];

interface MemberLevelCandidate {
  id?: unknown;
  name?: unknown;
  discountPercent?: unknown;
  status?: unknown;
  isDefault?: unknown;
  createdAt?: unknown;
  updatedAt?: unknown;
  revision?: unknown;
}

interface MemberLevelPageCandidate {
  data?: unknown;
  total?: unknown;
  current?: unknown;
  pageSize?: unknown;
  integrity?: unknown;
  operations?: unknown;
}

interface IntegrityCandidate {
  flaggedDefaultCount?: unknown;
  enabledDefaultCount?: unknown;
  invalidDefaultCount?: unknown;
}

interface OperationsCandidate {
  create?: unknown;
  update?: unknown;
  setDefault?: unknown;
  delete?: unknown;
}

export class MemberLevelContractError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'MemberLevelContractError';
  }
}

function isObject(value: unknown): value is object {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function assertExactKeys(value: object, expected: readonly string[], label: string): void {
  const actual = Object.keys(value);
  const expectedSet = new Set(expected);
  if (actual.length !== expected.length || actual.some((key) => !expectedSet.has(key))) {
    throw new MemberLevelContractError(`${label} contains missing or unsupported fields`);
  }
}

function recordCandidate(value: unknown): MemberLevelCandidate {
  if (!isObject(value)) {
    throw new MemberLevelContractError('Member level must be an object');
  }
  assertExactKeys(value, memberLevelKeys, 'Member level');
  return value as MemberLevelCandidate;
}

function pageCandidate(value: unknown): MemberLevelPageCandidate {
  if (!isObject(value)) {
    throw new MemberLevelContractError('Member level page must be an object');
  }
  assertExactKeys(value, memberLevelPageKeys, 'Member level page');
  return value as MemberLevelPageCandidate;
}

function requiredString(value: unknown, field: string, maxLength: number): string {
  if (typeof value !== 'string' || value.trim() === '' || value.length > maxLength) {
    throw new MemberLevelContractError(`Member level field ${field} is invalid`);
  }
  return value;
}

function memberLevelID(value: unknown, field = 'id'): string {
  const id = requiredString(value, field, 20);
  if (!/^[A-Za-z0-9_-]{1,20}$/.test(id)) {
    throw new MemberLevelContractError(`Member level field ${field} is invalid`);
  }
  return id;
}

function nameField(value: unknown): string {
  const name = requiredString(value, 'name', memberLevelNameMaxBytes);
  if (utf8ByteLength(name) > memberLevelNameMaxBytes) {
    throw new MemberLevelContractError('Member level field name is invalid');
  }
  return name;
}

function discountPercentInputField(value: unknown): string {
  if (typeof value !== 'string' || !/^(?:0|[1-9]\d?|100)(?:\.\d{1,2})?$/.test(value)) {
    throw new MemberLevelContractError('Member level field discountPercent is invalid');
  }
  const numeric = Number(value);
  if (!Number.isFinite(numeric) || numeric < 0 || numeric > 100) {
    throw new MemberLevelContractError('Member level field discountPercent is invalid');
  }
  return value;
}

function canonicalDiscountPercentField(value: unknown): string {
  const discountPercent = discountPercentInputField(value);
  const [integer, fraction = ''] = discountPercent.split('.');
  const normalizedFraction = fraction.replace(/0+$/, '');
  const canonical = normalizedFraction ? `${integer}.${normalizedFraction}` : integer;
  if (discountPercent !== canonical) {
    throw new MemberLevelContractError('Member level field discountPercent is not canonical');
  }
  return discountPercent;
}

function statusField(value: unknown): MemberLevelStatus {
  if (typeof value !== 'string' || !responseStatuses.includes(value as MemberLevelStatus)) {
    throw new MemberLevelContractError('Member level field status is invalid');
  }
  return value as MemberLevelStatus;
}

function writableStatusField(value: unknown): MemberLevelWritableStatus {
  if (typeof value !== 'string' || !writableStatuses.includes(value as MemberLevelWritableStatus)) {
    throw new MemberLevelContractError('Member level writable status is invalid');
  }
  return value as MemberLevelWritableStatus;
}

function booleanField(value: unknown, field: string): boolean {
  if (typeof value !== 'boolean') {
    throw new MemberLevelContractError(`Member level field ${field} is invalid`);
  }
  return value;
}

function timestampField(value: unknown, field: string): string {
  if (value === '') return '';
  const timestamp = requiredString(value, field, 64);
  if (!Number.isFinite(Date.parse(timestamp))) {
    throw new MemberLevelContractError(`Member level field ${field} is invalid`);
  }
  return timestamp;
}

function revisionField(value: unknown): string {
  if (typeof value !== 'string' || !/^[0-9a-f]{64}$/.test(value)) {
    throw new MemberLevelContractError('Member level field revision is invalid');
  }
  return value;
}

function positiveInteger(value: unknown, field: string): number {
  if (typeof value !== 'number' || !Number.isSafeInteger(value) || value <= 0) {
    throw new MemberLevelContractError(`Member level page field ${field} is invalid`);
  }
  return value;
}

function nonNegativeInteger(value: unknown, field: string): number {
  if (typeof value !== 'number' || !Number.isSafeInteger(value) || value < 0) {
    throw new MemberLevelContractError(`Member level page field ${field} is invalid`);
  }
  return value;
}

export function parseMemberLevel(value: unknown): MemberLevel {
  const candidate = recordCandidate(value);
  return {
    id: memberLevelID(candidate.id),
    name: nameField(candidate.name),
    discountPercent: canonicalDiscountPercentField(candidate.discountPercent),
    status: statusField(candidate.status),
    isDefault: booleanField(candidate.isDefault, 'isDefault'),
    createdAt: timestampField(candidate.createdAt, 'createdAt'),
    updatedAt: timestampField(candidate.updatedAt, 'updatedAt'),
    revision: revisionField(candidate.revision),
  };
}

export function parseMemberLevelPage(
  value: unknown,
  expected: Pick<MemberLevelQuery, 'current' | 'pageSize'>,
): MemberLevelPage {
  const candidate = pageCandidate(value);
  if (!Array.isArray(candidate.data)) {
    throw new MemberLevelContractError('Member level page data is invalid');
  }
  const data = candidate.data.map(parseMemberLevel);
  const total = nonNegativeInteger(candidate.total, 'total');
  const current = positiveInteger(candidate.current, 'current');
  const pageSize = positiveInteger(candidate.pageSize, 'pageSize');
  if (!isObject(candidate.integrity)) {
    throw new MemberLevelContractError('Member level page integrity is invalid');
  }
  assertExactKeys(candidate.integrity, integrityKeys, 'Member level page integrity');
  const integrityCandidate = candidate.integrity as IntegrityCandidate;
  const flaggedDefaultCount = nonNegativeInteger(
    integrityCandidate.flaggedDefaultCount,
    'flaggedDefaultCount',
  );
  const enabledDefaultCount = nonNegativeInteger(
    integrityCandidate.enabledDefaultCount,
    'enabledDefaultCount',
  );
  const invalidDefaultCount = nonNegativeInteger(
    integrityCandidate.invalidDefaultCount,
    'invalidDefaultCount',
  );
  if (
    enabledDefaultCount > flaggedDefaultCount ||
    invalidDefaultCount !== flaggedDefaultCount - enabledDefaultCount
  ) {
    throw new MemberLevelContractError('Member level page integrity counts are invalid');
  }
  if (!isObject(candidate.operations)) {
    throw new MemberLevelContractError('Member level page operations are invalid');
  }
  assertExactKeys(candidate.operations, operationKeys, 'Member level page operations');
  const operationsCandidate = candidate.operations as OperationsCandidate;
  const operations = {
    create: booleanField(operationsCandidate.create, 'operations.create'),
    update: booleanField(operationsCandidate.update, 'operations.update'),
    setDefault: booleanField(operationsCandidate.setDefault, 'operations.setDefault'),
    delete: booleanField(operationsCandidate.delete, 'operations.delete'),
  };
  if (
    current !== expected.current ||
    pageSize !== expected.pageSize ||
    pageSize > 100 ||
    data.length > pageSize ||
    data.length > total ||
    new Set(data.map((record) => record.id)).size !== data.length
  ) {
    throw new MemberLevelContractError('Member level page metadata is invalid');
  }
  return {
    data,
    total,
    current,
    pageSize,
    integrity: {
      flaggedDefaultCount,
      enabledDefaultCount,
      invalidDefaultCount,
    },
    operations,
  };
}

export function parseMemberLevelDeleteResponse(value: unknown): void {
  if (value === undefined || value === null || value === '') return;
  if (isObject(value) && Object.keys(value).length === 0) return;
  throw new MemberLevelContractError('Member level delete response is invalid');
}

export function buildMemberLevelQuery(
  current: number,
  pageSize: number,
  filters: MemberLevelFilterValues,
): MemberLevelQuery {
  if (
    !Number.isSafeInteger(current) ||
    current < 1 ||
    !Number.isSafeInteger(pageSize) ||
    pageSize < 1 ||
    pageSize > 100
  ) {
    throw new MemberLevelContractError('Member level query pagination is invalid');
  }
  if (current - 1 > Math.floor(Number.MAX_SAFE_INTEGER / pageSize)) {
    throw new MemberLevelContractError('Member level query pagination is invalid');
  }
  const query: MemberLevelQuery = { current, pageSize };
  if (typeof filters.q === 'string' && filters.q.trim()) {
    const q = filters.q.trim();
    if (utf8ByteLength(q) > memberLevelNameMaxBytes) {
      throw new MemberLevelContractError('Member level query name is invalid');
    }
    query.q = q;
  }
  if (filters.status && filters.status !== 'all') {
    query.status = writableStatusField(filters.status);
  }
  if (filters.isDefault === 'true') query.isDefault = true;
  if (filters.isDefault === 'false') query.isDefault = false;
  return query;
}

export function createMemberLevelInput(values: MemberLevelEditorValues): CreateMemberLevelInput {
  const input: CreateMemberLevelInput = {
    name: nameField(typeof values.name === 'string' ? values.name.trim() : values.name),
    discountPercent: discountPercentInputField(
      typeof values.discountPercent === 'string'
        ? values.discountPercent.trim()
        : values.discountPercent,
    ),
    status: writableStatusField(values.status),
  };
  if (values.paymentPolicySourceLevelId) {
    input.paymentPolicySourceLevelId = memberLevelID(
      values.paymentPolicySourceLevelId,
      'paymentPolicySourceLevelId',
    );
  }
  return input;
}

export function updateMemberLevelInput(
  values: MemberLevelEditorValues,
  revision: string,
): UpdateMemberLevelInput {
  const input = createMemberLevelInput(values);
  return {
    name: input.name,
    discountPercent: input.discountPercent,
    status: input.status,
    revision: revisionField(revision),
  };
}

export function memberLevelRevisionInput(revision: string): MemberLevelRevisionInput {
  return { revision: revisionField(revision) };
}

export function utf8ByteLength(value: string): number {
  return new TextEncoder().encode(value).length;
}

export function getMemberLevelCapabilities(user?: CurrentUser): MemberLevelCapabilities {
  const canAccessRoute = hasPermission(user, memberLevelsPermissionPaths.route);
  return {
    canList: canAccessRoute && hasPermission(user, memberLevelsPermissionPaths.list),
    canRead: canAccessRoute && hasPermission(user, memberLevelsPermissionPaths.read),
    canCreate: canAccessRoute && hasPermission(user, memberLevelsPermissionPaths.create),
    canUpdate: canAccessRoute && hasPermission(user, memberLevelsPermissionPaths.update),
    canSetDefault: canAccessRoute && hasPermission(user, memberLevelsPermissionPaths.setDefault),
    canDelete: canAccessRoute && hasPermission(user, memberLevelsPermissionPaths.delete),
  };
}

export function intersectMemberLevelOperations(
  capabilities: MemberLevelCapabilities,
  operations?: MemberLevelOperations,
): MemberLevelCapabilities {
  return {
    ...capabilities,
    canCreate: capabilities.canCreate && operations?.create === true,
    canUpdate: capabilities.canUpdate && operations?.update === true,
    canSetDefault: capabilities.canSetDefault && operations?.setDefault === true,
    canDelete: capabilities.canDelete && operations?.delete === true,
  };
}
