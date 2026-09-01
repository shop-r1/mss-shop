import { describe, expect, it } from 'vitest';
import enUS from '../locales/en-US';
import zhCN from '../locales/zh-CN';
import { localizeMallSettingsError } from './errors';

describe('mall settings write-disabled localization', () => {
  it('uses the stable bilingual message key', () => {
    const envelope = {
      response: {
        data: {
          errorCode: 'MALL_SETTINGS_WRITE_DISABLED',
          errorMessage: 'fallback',
          messageKey: 'mallSettings.errors.writeDisabled',
        },
      },
    };
    expect(localizeMallSettingsError(envelope, (id) => enUS[id], 'failed')).toBe(
      enUS['mallSettings.errors.writeDisabled'],
    );
    expect(localizeMallSettingsError(envelope, (id) => zhCN[id], '失败')).toBe(
      zhCN['mallSettings.errors.writeDisabled'],
    );
  });
});
