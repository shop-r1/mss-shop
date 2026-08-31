import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import type { RunTimeLayoutConfig } from '@umijs/max';
import { describe, expect, it, vi } from 'vitest';
import { withAuthorizedMenuLocale } from './menu-locale';

describe('withAuthorizedMenuLocale', () => {
  it('preserves the MSS layout and enables dynamic menu localization', () => {
    const request = vi.fn();
    const baseLayout = (() => ({
      layout: 'mix' as const,
      menu: {
        params: { authorizationVersion: 3 },
        request,
      },
    })) as unknown as RunTimeLayoutConfig;

    const layout = withAuthorizedMenuLocale(baseLayout);
    const result = layout({} as Parameters<RunTimeLayoutConfig>[0]);

    expect(result.layout).toBe('mix');
    expect(result.menu).toMatchObject({
      locale: true,
      params: { authorizationVersion: 3 },
      request,
    });
  });

  it('remains connected to the Blueprint-managed runtime facade', () => {
    const appFacade = readFileSync(resolve(process.cwd(), 'src/app.tsx'), 'utf8');

    expect(appFacade).toContain(
      "import { withAuthorizedMenuLocale } from './business/menu-locale'",
    );
    expect(appFacade).toContain('export const layout = withAuthorizedMenuLocale(mssLayout)');
  });
});
