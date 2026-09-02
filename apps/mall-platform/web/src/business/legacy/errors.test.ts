import { describe, expect, it, vi } from 'vitest';
import enUS from '../locales/en-US';
import zhCN from '../locales/zh-CN';
import { localizeLegacyError } from './errors';

const errorKeys = [
  'authenticationRequired',
  'forbidden',
  'authorizationUnavailable',
  'resourceNotFound',
  'recordNotFound',
  'conflict',
  'validationFailed',
  'schemaNotReady',
  'operationNotSupported',
  'invalidRequest',
  'internal',
  'requestFailed',
] as const;

describe('legacy business error localization', () => {
  it('prefers a known top-level messageKey and passes only primitive params', () => {
    const translate = vi.fn(
      (id: string, params?: Record<string, boolean | number | string>) =>
        `${id}:${params?.field ?? ''}`,
    );
    const result = localizeLegacyError(
      {
        response: {
          data: {
            errorCode: 'VALIDATION_FAILED',
            errorMessage: 'must not win',
            messageKey: 'legacy.errors.validationFailed',
            params: { field: 'name', nested: { unsafe: true } },
          },
        },
      },
      translate,
    );

    expect(result).toBe('legacy.errors.validationFailed:name');
    expect(translate).toHaveBeenCalledWith('legacy.errors.validationFailed', {
      field: 'name',
    });
  });

  it('keeps a normal backend message when no stable messageKey is known', () => {
    expect(
      localizeLegacyError(
        {
          data: {
            messageKey: 'untrusted.dynamic.key',
            errorMessage: 'Readable backend message',
          },
        },
        (id) => `translated:${id}`,
      ),
    ).toBe('Readable backend message');
    expect(localizeLegacyError(new Error('Network unavailable'), (id) => id)).toBe(
      'Network unavailable',
    );
  });

  it('supports the rollout nested key but never returns nested or malformed objects', () => {
    expect(
      localizeLegacyError(
        {
          response: {
            data: {
              error: { messageKey: 'legacy.errors.recordNotFound' },
            },
          },
        },
        (id) => `translated:${id}`,
      ),
    ).toBe('translated:legacy.errors.recordNotFound');

    for (const malformed of [
      { response: { data: { error: { message: { nested: true } } } } },
      { data: { message: { nested: true } } },
      { unknown: { deeply: ['nested'] } },
      null,
    ]) {
      const result = localizeLegacyError(malformed, (id) => `translated:${id}`);
      expect(typeof result).toBe('string');
      expect(result).toBe('translated:legacy.errors.requestFailed');
      expect(result).not.toContain('[object Object]');
    }
  });

  it('has stable Chinese and English text for every accepted message key', () => {
    for (const suffix of errorKeys) {
      expect(zhCN[`legacy.errors.${suffix}`]).toBeTruthy();
      expect(enUS[`legacy.errors.${suffix}`]).toBeTruthy();
    }
  });
});
