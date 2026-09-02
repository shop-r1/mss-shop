import { describe, expect, it } from 'vitest';
import {
  canUpdateMallGeneralSettings,
  mallGeneralSettingsInput,
  parseMallGeneralSettings,
} from './contract';

function responseFixture(update = false) {
  return {
    mallName: 'Aussibuy',
    orderPrefix: 'AB',
    defaultSenderName: 'Sender',
    defaultSenderPhone: '100',
    operations: { update },
  };
}

describe('mall settings response contract', () => {
  it('accepts the closed fields and stable server-projected update operation', () => {
    expect(parseMallGeneralSettings(responseFixture())).toEqual(responseFixture());
    expect(
      canUpdateMallGeneralSettings(
        { canRead: true, canUpdate: true },
        parseMallGeneralSettings(responseFixture(false)),
      ),
    ).toBe(false);
    expect(
      canUpdateMallGeneralSettings(
        { canRead: true, canUpdate: true },
        parseMallGeneralSettings(responseFixture(true)),
      ),
    ).toBe(true);
  });

  it('rejects missing, extra and malformed operation fields', () => {
    const missing = responseFixture() as Record<string, unknown>;
    delete missing.operations;
    expect(() => parseMallGeneralSettings(missing)).toThrow('missing or unsupported');
    expect(() =>
      parseMallGeneralSettings({
        ...responseFixture(),
        metadata: { secret: 'never' },
      }),
    ).toThrow('missing or unsupported');
    expect(() =>
      parseMallGeneralSettings({
        ...responseFixture(),
        operations: { update: 'false' },
      }),
    ).toThrow('operation update is invalid');
    expect(() =>
      parseMallGeneralSettings({
        ...responseFixture(),
        operations: { update: false, delete: false },
      }),
    ).toThrow('operations contain missing or unsupported');
  });

  it('never submits the server-projected operations object', () => {
    expect(mallGeneralSettingsInput(parseMallGeneralSettings(responseFixture(true)))).toEqual({
      mallName: 'Aussibuy',
      orderPrefix: 'AB',
      defaultSenderName: 'Sender',
      defaultSenderPhone: '100',
    });
  });
});
