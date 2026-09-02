import { describe, expect, it } from 'vitest';
import enUS from '../locales/en-US';
import zhCN from '../locales/zh-CN';
import { mallGeneralSettingsPermissionPaths } from '../mall-settings/paths';
import { memberLevelsPermissionPaths } from '../member-levels/paths';
import routeRegistrations from '../route-registrations';
import businessRoutes from '../routes.config';
import {
  findLegacyResourceByName,
  findLegacyResourceByPath,
  LEGACY_DOMAINS,
  LEGACY_RESOURCES,
} from './catalog';

describe('mall legacy resource catalog', () => {
  it('contains exactly the 50 mall-owned resources', () => {
    expect(LEGACY_RESOURCES).toHaveLength(50);
    expect(new Set(LEGACY_RESOURCES.map((entry) => entry.resource)).size).toBe(50);
    expect(new Set(LEGACY_RESOURCES.map((entry) => entry.path)).size).toBe(50);
    expect(findLegacyResourceByName('courier_links')?.domain).toBe('fulfillment');
  });

  it('only resolves allowlisted routes and normalizes trailing slashes', () => {
    const goods = findLegacyResourceByName('goods');
    expect(goods).toBeDefined();
    expect(findLegacyResourceByPath('/business/catalog/goods')).toBe(goods);
    expect(findLegacyResourceByPath('/business/catalog/goods///')).toBe(goods);
    expect(findLegacyResourceByPath('/business/catalog/not_allowlisted')).toBeUndefined();
  });

  it('registers typed workflows plus one route and permission projection per legacy resource', () => {
    expect(businessRoutes).toHaveLength(LEGACY_RESOURCES.length + 2);
    expect(routeRegistrations).toHaveLength(LEGACY_RESOURCES.length + 2);
    expect(businessRoutes).toContainEqual({
      path: mallGeneralSettingsPermissionPaths.route,
      component: '@/business/mall-settings',
      name: 'mallSettings',
    });
    expect(businessRoutes).toContainEqual({
      path: memberLevelsPermissionPaths.route,
      component: '@/business/member-levels',
      name: 'memberLevels',
    });
    expect(routeRegistrations).toContainEqual({
      path: mallGeneralSettingsPermissionPaths.route,
      serverPaths: [mallGeneralSettingsPermissionPaths.route],
      menuName: 'mallSettings',
      permission: mallGeneralSettingsPermissionPaths.route,
    });
    expect(routeRegistrations).toContainEqual({
      path: memberLevelsPermissionPaths.route,
      serverPaths: [memberLevelsPermissionPaths.route],
      menuName: 'memberLevels',
      permission: memberLevelsPermissionPaths.route,
    });

    for (const entry of LEGACY_RESOURCES) {
      expect(entry.routePermission).toBe(entry.path);
      expect(entry.listPermission).toBe(`${entry.path}/permissions/list`);
      expect(entry.readPermission).toBe(`${entry.path}/permissions/read`);
      expect(entry.createPermission).toBe(`${entry.path}/permissions/create`);
      expect(entry.updatePermission).toBe(`${entry.path}/permissions/update`);
      expect(entry.deletePermission).toBe(`${entry.path}/permissions/delete`);
      expect(businessRoutes).toContainEqual({
        path: entry.path,
        component: '@/business/legacy/ResourcePage',
        name: entry.menuName,
      });
      expect(routeRegistrations).toContainEqual({
        path: entry.path,
        serverPaths: [entry.path],
        menuName: entry.resource,
        permission: entry.routePermission,
      });
    }
  });

  it('provides complete Simplified Chinese and English domain and resource labels', () => {
    for (const domain of LEGACY_DOMAINS) {
      for (const messages of [zhCN, enUS]) {
        expect(messages[`legacy.domain.${domain}`]).toBeTruthy();
        expect(messages[`menu.${domain}`]).toBeTruthy();
        expect(messages[`menu.legacy.domain.${domain}`]).toBeTruthy();
        expect(messages[`menu.legacyBusiness.${domain}`]).toBeTruthy();
      }
    }

    for (const entry of LEGACY_RESOURCES) {
      for (const messages of [zhCN, enUS]) {
        expect(messages[entry.titleKey]).toBeTruthy();
        expect(messages[`legacy.resources.${entry.resource}`]).toBeTruthy();
        expect(messages[`menu.${entry.resource}`]).toBeTruthy();
        expect(messages[`menu.${entry.menuName}`]).toBeTruthy();
        expect(messages[`menu.legacyBusiness.${entry.domain}.${entry.resource}`]).toBeTruthy();
      }
    }
  });
});
