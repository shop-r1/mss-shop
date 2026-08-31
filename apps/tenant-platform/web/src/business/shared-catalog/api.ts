import { request } from '@umijs/max';
import type { SharedCatalogResource } from './catalog';
import { parseSharedCatalogListResponse, parseSharedCatalogRecordResponse } from './contract';
import type {
  SharedCatalogListQuery,
  SharedCatalogListResponse,
  SharedCatalogRecord,
} from './types';

export interface SharedCatalogRequestOptions {
  method: 'DELETE' | 'GET' | 'POST' | 'PUT';
  data?: unknown;
  params?: Record<string, unknown>;
  skipErrorHandler: true;
}

export type SharedCatalogRequester = (
  path: string,
  options: SharedCatalogRequestOptions,
) => Promise<unknown>;

function collectionPath(entry: SharedCatalogResource): string {
  return `/legacy/resources/${entry.resource}`;
}

function detailPath(entry: SharedCatalogResource, id: string): string {
  return `${collectionPath(entry)}/${encodeURIComponent(id)}`;
}

function assertWritable(entry: SharedCatalogResource): void {
  if (!entry.writable) {
    throw new Error(`shared catalog resource ${entry.resource} is read-only`);
  }
}

export function createSharedCatalogClient(requester: SharedCatalogRequester) {
  return {
    async list(
      entry: SharedCatalogResource,
      query: SharedCatalogListQuery,
    ): Promise<SharedCatalogListResponse> {
      const value = await requester(collectionPath(entry), {
        method: 'GET',
        params: {
          page: query.page,
          pageSize: query.pageSize,
          ...(query.q ? { q: query.q } : {}),
        },
        skipErrorHandler: true,
      });
      return parseSharedCatalogListResponse(value, entry);
    },

    async detail(entry: SharedCatalogResource, id: string): Promise<SharedCatalogRecord> {
      const value = await requester(detailPath(entry, id), {
        method: 'GET',
        skipErrorHandler: true,
      });
      return parseSharedCatalogRecordResponse(value, entry);
    },

    async create(
      entry: SharedCatalogResource,
      data: SharedCatalogRecord,
    ): Promise<SharedCatalogRecord> {
      assertWritable(entry);
      const value = await requester(collectionPath(entry), {
        method: 'POST',
        data,
        skipErrorHandler: true,
      });
      return parseSharedCatalogRecordResponse(value, entry);
    },

    async update(
      entry: SharedCatalogResource,
      id: string,
      data: SharedCatalogRecord,
    ): Promise<SharedCatalogRecord> {
      assertWritable(entry);
      const value = await requester(detailPath(entry, id), {
        method: 'PUT',
        data,
        skipErrorHandler: true,
      });
      return parseSharedCatalogRecordResponse(value, entry);
    },

    async remove(entry: SharedCatalogResource, id: string): Promise<void> {
      assertWritable(entry);
      await requester(detailPath(entry, id), {
        method: 'DELETE',
        skipErrorHandler: true,
      });
    },
  };
}

export const sharedCatalogClient = createSharedCatalogClient((path, options) =>
  request<unknown>(path, options),
);
