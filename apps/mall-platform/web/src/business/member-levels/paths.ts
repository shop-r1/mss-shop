// Umi loads this file while evaluating route configuration in Node. Keep it
// free of browser and Admin Web runtime imports.
export const memberLevelsResourcePath = '/member-levels';

export const memberLevelsPermissionPaths = {
  route: '/business/customers/member-levels',
  list: '/business/customers/member-levels/permissions/list',
  read: '/business/customers/member-levels/permissions/read',
  create: '/business/customers/member-levels/permissions/create',
  update: '/business/customers/member-levels/permissions/update',
  setDefault: '/business/customers/member-levels/permissions/set-default',
  delete: '/business/customers/member-levels/permissions/delete',
} as const;
