import { keepPreviousData, useQuery } from '@tanstack/react-query';
import { memberLevelsAPI } from './api';
import type { MemberLevelQuery } from './types';

export const memberLevelQueryKeys = {
  all: ['business', 'member-levels'] as const,
  lists: ['business', 'member-levels', 'list'] as const,
  list: (params: MemberLevelQuery) => ['business', 'member-levels', 'list', params] as const,
  details: ['business', 'member-levels', 'detail'] as const,
  detail: (id: string) => ['business', 'member-levels', 'detail', id] as const,
};

export function useMemberLevelPage(params: MemberLevelQuery, enabled = true) {
  return useQuery({
    queryKey: memberLevelQueryKeys.list(params),
    queryFn: () => memberLevelsAPI.loadPage(params),
    enabled,
    placeholderData: keepPreviousData,
    staleTime: 15_000,
  });
}

export function useMemberLevel(id?: string, enabled = true) {
  return useQuery({
    queryKey: memberLevelQueryKeys.detail(id ?? ''),
    queryFn: () => memberLevelsAPI.loadOne(id as string),
    enabled: enabled && Boolean(id),
    staleTime: 30_000,
  });
}
