import { describe, expect, it } from 'vitest';
import enUS from '../locales/en-US';
import zhCN from '../locales/zh-CN';
import routeRegistrations from '../route-registrations';
import businessRoutes from '../routes.config';
import {
  findSharedCatalogResourceByName,
  findSharedCatalogResourceByPath,
  SHARED_CATALOG_FIELDS,
  SHARED_CATALOG_RESOURCES,
} from './catalog';

describe('tenant shared catalog allowlist', () => {
  it('retains only the platform payment resource and keeps it read-only', () => {
    expect(SHARED_CATALOG_RESOURCES).toHaveLength(1);
    expect(
      SHARED_CATALOG_RESOURCES.filter((entry) => entry.writable).map((entry) => entry.resource),
    ).toEqual([]);
    expect(
      SHARED_CATALOG_RESOURCES.filter((entry) => !entry.writable).map((entry) => entry.resource),
    ).toEqual(['payments']);
  });

  it('locks all response domains and UI paths to shared-catalog', () => {
    for (const entry of SHARED_CATALOG_RESOURCES) {
      expect(entry.domain).toBe('shared-catalog');
      expect(entry.path).toBe(`/business/shared-catalog/${entry.resource}`);
      expect(entry.routePermission).toBe(entry.path);
      expect(entry.listPermission).toBe(`${entry.path}/permissions/list`);
      expect(entry.readPermission).toBe(`${entry.path}/permissions/read`);
      expect(entry.createPermission).toBe(`${entry.path}/permissions/create`);
      expect(entry.updatePermission).toBe(`${entry.path}/permissions/update`);
      expect(entry.deletePermission).toBe(`${entry.path}/permissions/delete`);
      expect(findSharedCatalogResourceByPath(`${entry.path}///`)).toBe(entry);
      expect(findSharedCatalogResourceByName(entry.resource)).toBe(entry);
    }
  });

  it('registers one route and one route-level permission projection per resource', () => {
    expect(businessRoutes).toHaveLength(1);
    expect(routeRegistrations).toHaveLength(1);
    for (const entry of SHARED_CATALOG_RESOURCES) {
      expect(businessRoutes).toContainEqual({
        path: entry.path,
        component: '@/business/shared-catalog/ResourcePage',
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

  it('provides complete Chinese and English resource, field, menu, and read-only text', () => {
    for (const messages of [zhCN, enUS]) {
      expect(messages['sharedCatalog.domain']).toBeTruthy();
      expect(messages['menu.sharedCatalog.domain']).toBeTruthy();
      expect(messages['menu.sharedCatalog']).toBeTruthy();
      for (const field of SHARED_CATALOG_FIELDS) {
        expect(messages[`legacy.fields.${field}`]).toBeTruthy();
      }
      for (const entry of SHARED_CATALOG_RESOURCES) {
        expect(messages[entry.titleKey]).toBeTruthy();
        expect(messages[`menu.${entry.resource}`]).toBeTruthy();
        expect(messages[`menu.${entry.menuName}`]).toBeTruthy();
        if (!entry.writable) {
          expect(entry.readOnlyReasonKey).toBeTruthy();
          expect(messages[entry.readOnlyReasonKey ?? '']).toBeTruthy();
        }
      }
    }
  });
});
