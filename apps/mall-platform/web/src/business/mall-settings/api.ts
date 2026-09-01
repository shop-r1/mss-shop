import { request } from '@umijs/max';
import { parseMallGeneralSettings } from './contract';
import { mallGeneralSettingsPath } from './paths';
import type { MallGeneralSettings, MallGeneralSettingsInput } from './types';

export interface MallGeneralSettingsRequestOptions {
  method: 'GET' | 'PUT';
  data?: MallGeneralSettingsInput;
  skipErrorHandler: true;
}

export type MallGeneralSettingsRequester = (
  path: string,
  options: MallGeneralSettingsRequestOptions,
) => Promise<unknown>;

export function createMallGeneralSettingsAPI(requester: MallGeneralSettingsRequester) {
  return {
    load: async (): Promise<MallGeneralSettings> =>
      parseMallGeneralSettings(
        await requester(mallGeneralSettingsPath, {
          method: 'GET',
          skipErrorHandler: true,
        }),
      ),

    update: async (input: MallGeneralSettingsInput): Promise<MallGeneralSettings> =>
      parseMallGeneralSettings(
        await requester(mallGeneralSettingsPath, {
          method: 'PUT',
          data: input,
          skipErrorHandler: true,
        }),
      ),
  };
}

export const mallGeneralSettingsAPI = createMallGeneralSettingsAPI((path, options) =>
  request<unknown>(path, options),
);
