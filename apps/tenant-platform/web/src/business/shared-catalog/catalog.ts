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
 * The fixed tenant-platform allowlist now retains only the source payment
 * catalogue. Product and logistics resources are tenant-owned mall data under
 * DEC-0009. Payment remains read-only while DEC-0008 is under review.
 */
export const SHARED_CATALOG_RESOURCES = [
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
  'logo',
  'site_url',
  'description',
  'status',
  'name',
  'method',
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
