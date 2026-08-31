import type { SharedCatalogDomain, SharedCatalogResourceName } from './catalog';

export type SharedCatalogRecord = Record<string, unknown>;

export interface SharedCatalogColumn {
  name: string;
  label: string;
  type: string;
  writable: boolean;
  secret: boolean;
  required: boolean;
}

export interface SharedCatalogCapabilities {
  detail: boolean;
  create: boolean;
  update: boolean;
  delete: boolean;
}

export interface SharedCatalogDescriptor {
  name: SharedCatalogResourceName;
  domain: SharedCatalogDomain;
  titleKey: string;
  columns: SharedCatalogColumn[];
  capabilities: SharedCatalogCapabilities;
}

export interface SharedCatalogListResponse {
  data: SharedCatalogRecord[];
  total: number;
  page: number;
  pageSize: number;
  resource: SharedCatalogDescriptor;
}

export interface SharedCatalogListQuery {
  page: number;
  pageSize: number;
  q?: string;
}
