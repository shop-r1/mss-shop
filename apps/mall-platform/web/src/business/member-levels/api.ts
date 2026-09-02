import { request } from '@umijs/max';
import {
  memberLevelRevisionInput,
  parseMemberLevel,
  parseMemberLevelDeleteResponse,
  parseMemberLevelPage,
} from './contract';
import { memberLevelsResourcePath } from './paths';
import type {
  CreateMemberLevelInput,
  MemberLevel,
  MemberLevelPage,
  MemberLevelQuery,
  MemberLevelRevisionInput,
  UpdateMemberLevelInput,
} from './types';

export interface MemberLevelsRequestOptions {
  method: 'DELETE' | 'GET' | 'POST' | 'PUT';
  data?: CreateMemberLevelInput | MemberLevelRevisionInput | UpdateMemberLevelInput;
  params?: MemberLevelQuery;
  skipErrorHandler: true;
}

export type MemberLevelsRequestClient = (
  path: string,
  options: MemberLevelsRequestOptions,
) => Promise<unknown>;

function memberLevelPath(id: string): string {
  return `${memberLevelsResourcePath}/${encodeURIComponent(id)}`;
}

export function createMemberLevelsAPI(client: MemberLevelsRequestClient) {
  return {
    loadPage: async (params: MemberLevelQuery): Promise<MemberLevelPage> =>
      parseMemberLevelPage(
        await client(memberLevelsResourcePath, {
          method: 'GET',
          params,
          skipErrorHandler: true,
        }),
        params,
      ),

    loadOne: async (id: string): Promise<MemberLevel> =>
      parseMemberLevel(
        await client(memberLevelPath(id), {
          method: 'GET',
          skipErrorHandler: true,
        }),
      ),

    create: async (input: CreateMemberLevelInput): Promise<MemberLevel> =>
      parseMemberLevel(
        await client(memberLevelsResourcePath, {
          method: 'POST',
          data: input,
          skipErrorHandler: true,
        }),
      ),

    update: async (id: string, input: UpdateMemberLevelInput): Promise<MemberLevel> =>
      parseMemberLevel(
        await client(memberLevelPath(id), {
          method: 'PUT',
          data: input,
          skipErrorHandler: true,
        }),
      ),

    setDefault: async (id: string, revision: string): Promise<MemberLevel> =>
      parseMemberLevel(
        await client(`${memberLevelPath(id)}/default`, {
          method: 'PUT',
          data: memberLevelRevisionInput(revision),
          skipErrorHandler: true,
        }),
      ),

    remove: async (id: string, revision: string): Promise<void> =>
      parseMemberLevelDeleteResponse(
        await client(memberLevelPath(id), {
          method: 'DELETE',
          data: memberLevelRevisionInput(revision),
          skipErrorHandler: true,
        }),
      ),
  };
}

export const memberLevelsAPI = createMemberLevelsAPI((path, options) =>
  request<unknown>(path, options),
);
