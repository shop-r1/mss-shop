import { request } from '@umijs/max';
import type { LegacyResourceEntry } from './catalog';
import { parseLegacyRecordResponse, parseLegacyResourceListResponse } from './contract';
import type { LegacyListQuery, LegacyRecord, LegacyResourceListResponse } from './types';

interface LegacyRequestOptions {
  method?: 'GET' | 'POST' | 'PUT' | 'DELETE';
  params?: Record<string, unknown>;
  data?: unknown;
}

export type LegacyRequester = (url: string, options?: LegacyRequestOptions) => Promise<unknown>;

function collectionPath(entry: LegacyResourceEntry): string {
  return `/legacy/resources/${entry.resource}`;
}

function detailPath(entry: LegacyResourceEntry, id: string): string {
  return `${collectionPath(entry)}/${encodeURIComponent(id)}`;
}

export function createLegacyResourceClient(requester: LegacyRequester) {
  return {
    async list(
      entry: LegacyResourceEntry,
      query: LegacyListQuery,
    ): Promise<LegacyResourceListResponse> {
      const value = await requester(collectionPath(entry), {
        method: 'GET',
        params: {
          page: query.page,
          pageSize: query.pageSize,
          ...(query.q ? { q: query.q } : {}),
        },
      });
      return parseLegacyResourceListResponse(value, entry);
    },

    async detail(entry: LegacyResourceEntry, id: string): Promise<LegacyRecord> {
      const value = await requester(detailPath(entry, id), { method: 'GET' });
      return parseLegacyRecordResponse(value, entry);
    },

    async create(entry: LegacyResourceEntry, data: LegacyRecord): Promise<void> {
      await requester(collectionPath(entry), { method: 'POST', data });
    },

    async update(entry: LegacyResourceEntry, id: string, data: LegacyRecord): Promise<void> {
      await requester(detailPath(entry, id), { method: 'PUT', data });
    },

    async remove(entry: LegacyResourceEntry, id: string): Promise<void> {
      await requester(detailPath(entry, id), { method: 'DELETE' });
    },
  };
}

const defaultRequester: LegacyRequester = (url, options) => request<unknown>(url, options ?? {});

export const legacyResourceClient = createLegacyResourceClient(defaultRequester);
