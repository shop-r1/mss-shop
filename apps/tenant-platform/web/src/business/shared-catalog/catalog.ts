export const SHARED_CATALOG_DOMAINS = ['shared-catalog'] as const;

export type SharedCatalogDomain = (typeof SHARED_CATALOG_DOMAINS)[number];

export interface SharedCatalogResourceEntry<Resource extends string = string> {
  readonly domain: 'shared-catalog';
  readonly resource: Resource;
  readonly writable: boolean;
  readonly path: `/business/shared-catalog/${Resource}`;
  readonly menuName: `sharedCatalog.${Resource}`;
  readonly titleKey: `legacy.resources.${Resource}`;
  readonly domainTitleKey: 'sharedCatalog.domain';
  readonly readOnlyReasonKey?: `sharedCatalog.readOnly.${Resource}`;
  readonly routePermission: `/business/shared-catalog/${Resource}`;
  readonly listPermission: `/business/shared-catalog/${Resource}/permissions/list`;
  readonly readPermission: `/business/shared-catalog/${Resource}/permissions/read`;
  readonly createPermission: `/business/shared-catalog/${Resource}/permissions/create`;
  readonly updatePermission: `/business/shared-catalog/${Resource}/permissions/update`;
  readonly deletePermission: `/business/shared-catalog/${Resource}/permissions/delete`;
}

function resource<const Resource extends string>(
  name: Resource,
  writable: boolean,
): SharedCatalogResourceEntry<Resource> {
  return {
    domain: 'shared-catalog',
    resource: name,
    writable,
    path: `/business/shared-catalog/${name}`,
    menuName: `sharedCatalog.${name}`,
    titleKey: `legacy.resources.${name}`,
    domainTitleKey: 'sharedCatalog.domain',
    ...(writable
      ? {}
      : {
          readOnlyReasonKey: `sharedCatalog.readOnly.${name}` as const,
        }),
    routePermission: `/business/shared-catalog/${name}`,
    listPermission: `/business/shared-catalog/${name}/permissions/list`,
    readPermission: `/business/shared-catalog/${name}/permissions/read`,
    createPermission: `/business/shared-catalog/${name}/permissions/create`,
    updatePermission: `/business/shared-catalog/${name}/permissions/update`,
    deletePermission: `/business/shared-catalog/${name}/permissions/delete`,
  };
}

/**
 * The fixed eight-resource shared catalogue allowlist. Every resource remains
 * read-only until a dedicated workflow owns its legacy hooks, cache
 * invalidation, and cross-tenant side effects. This list is never database
 * discovery or a SQL selector.
 */
export const SHARED_CATALOG_RESOURCES = [
  resource('brands', false),
  resource('categories', false),
  resource('classes', false),
  resource('goods_infos', false),
  resource('couriers', false),
  resource('courier_pack_rules', false),
  resource('courier_links', false),
  resource('payments', false),
] as const;

export type SharedCatalogResourceName = (typeof SHARED_CATALOG_RESOURCES)[number]['resource'];
export type SharedCatalogResource = (typeof SHARED_CATALOG_RESOURCES)[number];

/** Field locale keys emitted by the reviewed backend manifest. */
export const SHARED_CATALOG_FIELDS = [
  'id',
  'created_at',
  'updated_at',
  'deleted_at',
  'name_zh',
  'name_en',
  'logo',
  'site_url',
  'index_img',
  'bg_img',
  'description',
  'sort',
  'status',
  'parent_id',
  'name',
  'alias',
  'img',
  'tag',
  'pack_rule',
  'category_id',
  'attributes',
  'parent_category_id',
  'brand_id',
  'album',
  'image',
  'video',
  'keywords',
  'bar_code',
  'content',
  'weight',
  'has_pack_rule',
  'unit',
  'goods_type',
  'region',
  'method',
  'courier_id',
  'simple',
  'mixed',
  'mixed_sum',
  'price_unit',
  'price_total',
  'link_id',
  'left_rule_id',
  'object_ids_data',
  'type',
  'terminals',
] as const;

export type SharedCatalogFieldName = (typeof SHARED_CATALOG_FIELDS)[number];

const resourcesByPath = new Map<string, SharedCatalogResource>(
  SHARED_CATALOG_RESOURCES.map((entry) => [entry.path, entry]),
);

const resourcesByName = new Map<string, SharedCatalogResource>(
  SHARED_CATALOG_RESOURCES.map((entry) => [entry.resource, entry]),
);

export function findSharedCatalogResourceByPath(
  pathname: string,
): SharedCatalogResource | undefined {
  const normalized = pathname.length > 1 ? pathname.replace(/\/+$/, '') : pathname;
  return resourcesByPath.get(normalized);
}

export function findSharedCatalogResourceByName(name: string): SharedCatalogResource | undefined {
  return resourcesByName.get(name);
}
