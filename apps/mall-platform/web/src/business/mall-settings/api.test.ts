import { describe, expect, it, vi } from 'vitest';
import { createMallGeneralSettingsAPI, type MallGeneralSettingsRequester } from './api';

const response = {
  mallName: 'Aussibuy',
  orderPrefix: 'AB',
  defaultSenderName: 'Sender',
  defaultSenderPhone: '100',
  operations: { update: false },
};

describe('mall settings API', () => {
  it('parses GET capability state and omits it from PUT input', async () => {
    const requester = vi.fn<MallGeneralSettingsRequester>().mockResolvedValue(response);
    const api = createMallGeneralSettingsAPI(requester);

    await expect(api.load()).resolves.toEqual(response);
    await expect(
      api.update({
        mallName: 'Aussibuy',
        orderPrefix: 'AB',
        defaultSenderName: 'Sender',
        defaultSenderPhone: '100',
      }),
    ).resolves.toEqual(response);
    expect(requester.mock.calls).toEqual([
      ['/mall-settings/general', { method: 'GET', skipErrorHandler: true }],
      [
        '/mall-settings/general',
        {
          method: 'PUT',
          data: {
            mallName: 'Aussibuy',
            orderPrefix: 'AB',
            defaultSenderName: 'Sender',
            defaultSenderPhone: '100',
          },
          skipErrorHandler: true,
        },
      ],
    ]);
  });
});
