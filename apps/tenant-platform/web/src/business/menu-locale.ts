import type { RunTimeLayoutConfig } from '@umijs/max';

/** Enable hierarchical `menu.*` locale keys for MSS authorized menus. */
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
