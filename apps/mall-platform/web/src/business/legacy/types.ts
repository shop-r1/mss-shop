import type { LegacyDomain, LegacyResourceName } from './catalog';

export type LegacyRecord = Record<string, unknown>;

export interface LegacyResourceColumn {
  name: string;
  label: string;
  type: string;
  writable: boolean;
  secret: boolean;
  required: boolean;
}

export interface LegacyResourceCapabilities {
  detail: boolean;
  create: boolean;
  update: boolean;
  delete: boolean;
}

export interface LegacyResourceDescriptor {
  name: LegacyResourceName;
  domain: LegacyDomain;
  titleKey: string;
  columns: LegacyResourceColumn[];
  capabilities: LegacyResourceCapabilities;
}

export interface LegacyResourceListResponse {
  data: LegacyRecord[];
  total: number;
  page: number;
  pageSize: number;
  resource: LegacyResourceDescriptor;
}

export interface LegacyListQuery {
  page: number;
  pageSize: number;
  q?: string;
}
