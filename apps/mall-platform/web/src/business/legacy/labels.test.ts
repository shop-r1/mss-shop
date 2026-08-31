import { describe, expect, it, vi } from 'vitest';
import { humanizeLegacyFieldName, localizeLegacyFieldLabel } from './labels';
import type { LegacyResourceColumn } from './types';

function column(name: string, label = `legacy.fields.${name}`): LegacyResourceColumn {
  return {
    name,
    label,
    type: 'string',
    writable: false,
    secret: false,
    required: false,
  };
}

describe('legacy field labels', () => {
  it('uses the stable backend column label as the locale key', () => {
    const formatMessage = vi.fn(() => '商品编号');

    expect(localizeLegacyFieldLabel(column('goods_id'), formatMessage)).toBe('商品编号');
    expect(formatMessage).toHaveBeenCalledWith({
      id: 'legacy.fields.goods_id',
      defaultMessage: 'Goods ID',
    });
  });

  it('humanizes unknown future fields and common acronyms', () => {
    expect(humanizeLegacyFieldName('future_external_order_id')).toBe('Future External Order ID');
    expect(humanizeLegacyFieldName('callback_url')).toBe('Callback URL');
    expect(localizeLegacyFieldLabel(column('future_external_order_id'), () => '')).toBe(
      'Future External Order ID',
    );
  });

  it('never returns a raw, invalid, or failed message key', () => {
    expect(localizeLegacyFieldLabel(column('future_field'), ({ id }) => id)).toBe('Future Field');
    expect(
      localizeLegacyFieldLabel(column('future_field', 'legacy.column.future'), () => 'x'),
    ).toBe('Future Field');
    expect(
      localizeLegacyFieldLabel(column('future_field'), () => {
        throw new Error('missing catalog');
      }),
    ).toBe('Future Field');
  });
});
