import {
  getInitialState,
  innerProvider,
  layout as mssLayout,
  request,
} from '@mss-boot-io/admin-web/runtime/app';
import { withAuthorizedMenuLocale } from './business/menu-locale';

export { getInitialState, innerProvider, request };

export const layout = withAuthorizedMenuLocale(mssLayout);
