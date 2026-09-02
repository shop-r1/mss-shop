export type MemberLevelStatus = 'disabled' | 'enabled' | 'unknown';

export type MemberLevelWritableStatus = Exclude<MemberLevelStatus, 'unknown'>;

export interface MemberLevel {
  id: string;
  name: string;
  discountPercent: string;
  status: MemberLevelStatus;
  isDefault: boolean;
  createdAt: string;
  updatedAt: string;
  revision: string;
}

export interface MemberLevelQuery {
  current: number;
  pageSize: number;
  q?: string;
  status?: MemberLevelWritableStatus;
  isDefault?: boolean;
}

export interface MemberLevelPage {
  data: MemberLevel[];
  total: number;
  current: number;
  pageSize: number;
  integrity: {
    flaggedDefaultCount: number;
    enabledDefaultCount: number;
    invalidDefaultCount: number;
  };
  operations: MemberLevelOperations;
}

export interface MemberLevelOperations {
  create: boolean;
  update: boolean;
  setDefault: boolean;
  delete: boolean;
}

export interface MemberLevelFilterValues {
  q?: string;
  status?: 'all' | MemberLevelWritableStatus;
  isDefault?: 'all' | 'false' | 'true';
}

export interface CreateMemberLevelInput {
  name: string;
  discountPercent: string;
  status: MemberLevelWritableStatus;
  paymentPolicySourceLevelId?: string;
}

export interface UpdateMemberLevelInput {
  name: string;
  discountPercent: string;
  status: MemberLevelWritableStatus;
  revision: string;
}

export interface MemberLevelRevisionInput {
  revision: string;
}

export interface MemberLevelEditorValues {
  name?: string;
  discountPercent?: string;
  status?: MemberLevelWritableStatus;
  paymentPolicySourceLevelId?: string;
}

export interface MemberLevelCapabilities {
  canList: boolean;
  canRead: boolean;
  canCreate: boolean;
  canUpdate: boolean;
  canSetDefault: boolean;
  canDelete: boolean;
}

export interface MemberLevelReferenceCounts {
  count: string;
  members: string;
  activities: string;
  couponTemplates: string;
  goodsPrices: string;
}
