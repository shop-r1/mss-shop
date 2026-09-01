import { describe, expect, it, vi } from 'vitest';
import enUS from '../locales/en-US';
import zhCN from '../locales/zh-CN';
import { localizeMemberLevelError, type MemberLevelErrorParameters } from './errors';

describe('member-level error localization', () => {
  it('passes flattened typed reference counts and drops nested untrusted values', () => {
    const translate = vi.fn((id: string, parameters?: MemberLevelErrorParameters) =>
      parameters ? `${id}:${parameters.members}` : id,
    );
    const result = localizeMemberLevelError(
      {
        response: {
          data: {
            messageKey: 'memberLevels.errors.inUse',
            params: {
              count: 17,
              members: 2,
              activities: 3,
              couponTemplates: 5,
              goodsPrices: 7,
              nested: { secret: true },
            },
          },
        },
      },
      translate,
      'fallback',
    );

    expect(result).toBe('memberLevels.errors.inUse:2');
    expect(translate).toHaveBeenCalledWith(
      'memberLevels.errors.inUse',
      expect.objectContaining({
        count: '17',
        members: '2',
        activities: '3',
        couponTemplates: '5',
        goodsPrices: '7',
      }),
    );
    expect(translate.mock.calls[0]?.[1]).not.toHaveProperty('nested');
    expect(translate.mock.calls[0]?.[1]).not.toHaveProperty('references');
  });

  it('supports the prior nested reference envelope without passing the object itself', () => {
    const translate = vi.fn(
      (id: string, parameters?: MemberLevelErrorParameters) => `${id}:${parameters?.goodsPrices}`,
    );
    expect(
      localizeMemberLevelError(
        {
          data: {
            messageKey: 'memberLevels.errors.inUse',
            params: {
              references: {
                members: 1,
                activities: 2,
                couponTemplates: 3,
                goodsPrices: 4,
              },
            },
          },
        },
        translate,
        'fallback',
      ),
    ).toBe('memberLevels.errors.inUse:4');
    expect(translate.mock.calls[0]?.[1]).toMatchObject({
      members: '1',
      activities: '2',
      couponTemplates: '3',
      goodsPrices: '4',
    });
  });

  it('does not translate an untrusted message key and keeps a readable stable fallback', () => {
    const translate = vi.fn((id: string) => `translated:${id}`);
    expect(
      localizeMemberLevelError(
        {
          data: {
            messageKey: 'memberLevels.errors.attackerControlled',
            errorMessage: 'Readable backend message',
          },
        },
        translate,
        'fallback',
      ),
    ).toBe('Readable backend message');
    expect(translate).not.toHaveBeenCalled();
    expect(localizeMemberLevelError(null, (id) => id, 'fallback')).toBe('fallback');
  });

  it('has Chinese and English text for all newly stable safety errors', () => {
    for (const key of [
      'memberLevels.errors.defaultRequired',
      'memberLevels.errors.defaultRepairRequired',
      'memberLevels.errors.paymentPolicySource',
      'memberLevels.errors.inUse',
      'memberLevels.errors.schemaNotReady',
      'memberLevels.errors.mutationDisabled',
    ] as const) {
      expect(enUS[key]).toBeTruthy();
      expect(zhCN[key]).toBeTruthy();
    }
  });
});
