import type { RunTimeLayoutConfig } from '@umijs/max';

/**
 * MSS 1.3.7 leaves dynamic authorized-menu localization disabled by default.
 * Keep the framework layout intact while enabling hierarchical `menu.*` keys
 * supplied by this host's business locale catalog.
 */
export function withAuthorizedMenuLocale(baseLayout: RunTimeLayoutConfig): RunTimeLayoutConfig {
  return (initialData) => {
    const config = baseLayout(initialData);

    return {
      ...config,
      menu: {
        ...config.menu,
        locale: true,
      },
    };
  };
}
