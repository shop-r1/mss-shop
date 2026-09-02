// This file is imported by Umi configuration at Node startup. Keep it free of
// browser/runtime package imports so config evaluation never loads Admin Web's
// TypeScript runtime entry from node_modules.
export const mallGeneralSettingsPath = '/mall-settings/general';

export const mallGeneralSettingsPermissionPaths = {
  route: '/business/settings/mall-settings',
  read: '/business/settings/mall-settings/permissions/read',
  update: '/business/settings/mall-settings/permissions/update',
} as const;
