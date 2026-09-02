import type { AdminBusinessRoute } from '@mss-boot-io/admin-web/business';
import { SHARED_CATALOG_RESOURCES } from './shared-catalog/catalog';

// Add handwritten Umi business routes here. Generated AdminModule routes are
// composed ahead of this array by the managed config/business-routes.ts facade.
const businessRoutes: AdminBusinessRoute[] = SHARED_CATALOG_RESOURCES.map((entry) => ({
  path: entry.path,
  component: '@/business/shared-catalog/ResourcePage',
  name: entry.menuName,
}));

export default businessRoutes;
