import { describe, expect, it, vi } from 'vitest';
import enUS from '../locales/en-US';
import zhCN from '../locales/zh-CN';
import { localizeSharedCatalogError } from './errors';

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

describe('shared catalog error localization', () => {
  it('maps a known top-level legacy messageKey to a stable shared catalog key', () => {
    const translate = vi.fn(
      (id: string, params?: Record<string, boolean | number | string>) =>
        `${id}:${params?.field ?? ''}`,
    );
    const result = localizeSharedCatalogError(
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

    expect(result).toBe('sharedCatalog.errors.validationFailed:name');
    expect(translate).toHaveBeenCalledWith('sharedCatalog.errors.validationFailed', {
      field: 'name',
    });
  });

  it('keeps a normal backend message when no stable messageKey is known', () => {
    expect(
      localizeSharedCatalogError(
        {
          data: {
            messageKey: 'untrusted.dynamic.key',
            message: 'Readable backend message',
          },
        },
        (id) => `translated:${id}`,
      ),
    ).toBe('Readable backend message');
    expect(localizeSharedCatalogError(new Error('Network unavailable'), (id) => id)).toBe(
      'Network unavailable',
    );
  });

  it('supports rollout nesting but always converts malformed objects to a string', () => {
    expect(
      localizeSharedCatalogError(
        {
          response: {
            data: {
              error: { messageKey: 'legacy.errors.schemaNotReady' },
            },
          },
        },
        (id) => `translated:${id}`,
      ),
    ).toBe('translated:sharedCatalog.errors.schemaNotReady');

    for (const malformed of [
      { response: { data: { error: { message: { nested: true } } } } },
      { data: { errorMessage: { nested: true } } },
      { unknown: { deeply: ['nested'] } },
      undefined,
    ]) {
      const result = localizeSharedCatalogError(malformed, (id) => `translated:${id}`);
      expect(typeof result).toBe('string');
      expect(result).toBe('translated:sharedCatalog.errors.requestFailed');
      expect(result).not.toContain('[object Object]');
    }
  });

  it('has stable Chinese and English text for every mapped message key', () => {
    for (const suffix of errorKeys) {
      expect(zhCN[`sharedCatalog.errors.${suffix}`]).toBeTruthy();
      expect(enUS[`sharedCatalog.errors.${suffix}`]).toBeTruthy();
    }
  });
});
