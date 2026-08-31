import type { AdminBusinessRoute } from '@mss-boot-io/admin-web/business';
import { LEGACY_RESOURCES } from './legacy/catalog';

// Add handwritten Umi business routes here. Generated AdminModule routes are
// composed ahead of this array by the managed config/business-routes.ts facade.
const businessRoutes: AdminBusinessRoute[] = LEGACY_RESOURCES.map((entry) => ({
  path: entry.path,
  component: '@/business/legacy/ResourcePage',
  name: entry.menuName,
}));

export default businessRoutes;
