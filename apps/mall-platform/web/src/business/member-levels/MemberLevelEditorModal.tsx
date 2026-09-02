import { useIntl } from '@umijs/max';
import { Alert, Collapse, Form, Input, Modal, Select } from 'antd';
import { memberLevelNameMaxBytes, utf8ByteLength } from './contract';
import type { MemberLevel, MemberLevelEditorValues, MemberLevelWritableStatus } from './types';

interface MemberLevelEditorModalProps {
  error?: string;
  errorIsConflict: boolean;
  open: boolean;
  record?: MemberLevel;
  saving: boolean;
  session: number;
  sourceLevels: MemberLevel[];
  onCancel: () => void;
  onSubmit: (values: MemberLevelEditorValues) => void;
}

const discountPattern = /^(?:0|[1-9]\d?|100)(?:\.\d{1,2})?$/;

export default function MemberLevelEditorModal({
  error,
  errorIsConflict,
  open,
  record,
  saving,
  session,
  sourceLevels,
  onCancel,
  onSubmit,
}: MemberLevelEditorModalProps) {
  const intl = useIntl();
  const enabledDefault = record?.isDefault === true && record.status === 'enabled';
  const invalidFlaggedDefault = record?.isDefault === true && record.status !== 'enabled';
  const statusOptions: {
    disabled?: boolean;
    label: string;
    value: MemberLevelWritableStatus;
  }[] = [
    {
      value: 'enabled',
      label: intl.formatMessage({ id: 'memberLevels.values.status.enabled' }),
      disabled: invalidFlaggedDefault,
    },
    {
      value: 'disabled',
      label: intl.formatMessage({ id: 'memberLevels.values.status.disabled' }),
      disabled: enabledDefault,
    },
  ];
  const initialValues: MemberLevelEditorValues = record
    ? {
        name: record.name,
        discountPercent: record.discountPercent,
        status: record.status === 'unknown' ? undefined : record.status,
      }
    : { status: 'enabled' };
  const sourceOptions = sourceLevels.map((level) => ({
    value: level.id,
    label: level.isDefault
      ? intl.formatMessage(
          { id: 'memberLevels.editor.paymentPolicySourceDefaultOption' },
          { name: level.name },
        )
      : level.name,
  }));

  return (
    <Modal
      cancelButtonProps={{ disabled: saving }}
      cancelText={intl.formatMessage({ id: 'memberLevels.actions.cancel' })}
      destroyOnHidden
      mask={{ closable: false }}
      okButtonProps={{
        form: 'member-level-editor',
        htmlType: 'submit',
        loading: saving,
      }}
      okText={intl.formatMessage({ id: 'memberLevels.actions.save' })}
      onCancel={onCancel}
      open={open}
      title={intl.formatMessage({
        id: record ? 'memberLevels.editor.editTitle' : 'memberLevels.editor.createTitle',
      })}
      width={680}
    >
      <Form<MemberLevelEditorValues>
        initialValues={initialValues}
        key={session}
        layout="vertical"
        name="member-level-editor"
        onFinish={onSubmit}
        preserve={false}
        requiredMark="optional"
      >
        {invalidFlaggedDefault ? (
          <Alert
            description={intl.formatMessage({
              id: 'memberLevels.states.defaultFlaggedRepairDescription',
            })}
            showIcon
            style={{ marginBottom: 20 }}
            title={intl.formatMessage({
              id: 'memberLevels.states.defaultFlaggedRepairRequired',
            })}
            type="warning"
          />
        ) : record?.status === 'unknown' ? (
          <Alert
            description={intl.formatMessage({
              id: 'memberLevels.states.unknownRepairDescription',
            })}
            showIcon
            style={{ marginBottom: 20 }}
            title={intl.formatMessage({
              id: 'memberLevels.states.unknownRepairRequired',
            })}
            type="warning"
          />
        ) : null}
        {error ? (
          <Alert
            description={error}
            showIcon
            style={{ marginBottom: 20 }}
            title={intl.formatMessage({
              id: errorIsConflict
                ? 'memberLevels.states.conflict'
                : 'memberLevels.states.saveError',
            })}
            type={errorIsConflict ? 'warning' : 'error'}
          />
        ) : null}
        <Form.Item
          label={intl.formatMessage({ id: 'memberLevels.fields.name' })}
          name="name"
          rules={[
            {
              validator: (_rule: unknown, value: unknown) => {
                if (typeof value !== 'string' || value.trim() === '') {
                  return Promise.reject(
                    new Error(
                      intl.formatMessage({
                        id: 'memberLevels.validation.nameRequired',
                      }),
                    ),
                  );
                }
                if (utf8ByteLength(value.trim()) > memberLevelNameMaxBytes) {
                  return Promise.reject(
                    new Error(
                      intl.formatMessage(
                        { id: 'memberLevels.validation.nameMaxBytes' },
                        { max: memberLevelNameMaxBytes },
                      ),
                    ),
                  );
                }
                return Promise.resolve();
              },
            },
          ]}
        >
          <Input
            allowClear
            autoComplete="off"
            maxLength={memberLevelNameMaxBytes}
            placeholder={intl.formatMessage({
              id: 'memberLevels.fields.namePlaceholder',
            })}
          />
        </Form.Item>
        <Form.Item
          extra={intl.formatMessage({
            id: 'memberLevels.fields.discountPercentHelp',
          })}
          label={intl.formatMessage({
            id: 'memberLevels.fields.discountPercent',
          })}
          name="discountPercent"
          rules={[
            {
              validator: (_rule: unknown, value: unknown) => {
                if (typeof value !== 'string' || value.trim() === '') {
                  return Promise.reject(
                    new Error(
                      intl.formatMessage({
                        id: 'memberLevels.validation.discountRequired',
                      }),
                    ),
                  );
                }
                const candidate = value.trim();
                if (!discountPattern.test(candidate) || Number(candidate) > 100) {
                  return Promise.reject(
                    new Error(
                      intl.formatMessage({
                        id: 'memberLevels.validation.discountRange',
                      }),
                    ),
                  );
                }
                return Promise.resolve();
              },
            },
          ]}
        >
          <Input
            addonAfter="%"
            autoComplete="off"
            inputMode="decimal"
            placeholder={intl.formatMessage({
              id: 'memberLevels.fields.discountPercentPlaceholder',
            })}
          />
        </Form.Item>
        <Form.Item
          extra={
            enabledDefault
              ? intl.formatMessage({
                  id: 'memberLevels.editor.defaultStatusHelp',
                })
              : invalidFlaggedDefault
                ? intl.formatMessage({
                    id: 'memberLevels.editor.invalidDefaultStatusHelp',
                  })
                : undefined
          }
          label={intl.formatMessage({ id: 'memberLevels.fields.status' })}
          name="status"
          rules={[
            {
              required: true,
              message: intl.formatMessage({
                id: 'memberLevels.validation.statusRequired',
              }),
            },
          ]}
        >
          <Select
            options={statusOptions}
            placeholder={intl.formatMessage({
              id: 'memberLevels.fields.statusPlaceholder',
            })}
            virtual={false}
          />
        </Form.Item>
        {!record ? (
          <Collapse
            ghost
            items={[
              {
                key: 'payment-policy-source',
                label: intl.formatMessage({
                  id: 'memberLevels.editor.advancedTitle',
                }),
                children: (
                  <>
                    <Alert
                      description={intl.formatMessage({
                        id: 'memberLevels.editor.paymentPolicySourceDescription',
                      })}
                      showIcon
                      style={{ marginBottom: 16 }}
                      type="info"
                    />
                    <Form.Item
                      extra={intl.formatMessage({
                        id: 'memberLevels.editor.paymentPolicySourceHelp',
                      })}
                      label={intl.formatMessage({
                        id: 'memberLevels.editor.paymentPolicySourceLabel',
                      })}
                      name="paymentPolicySourceLevelId"
                    >
                      <Select
                        allowClear
                        options={sourceOptions}
                        placeholder={intl.formatMessage({
                          id: 'memberLevels.editor.paymentPolicySourcePlaceholder',
                        })}
                        showSearch
                        optionFilterProp="label"
                        virtual={false}
                      />
                    </Form.Item>
                  </>
                ),
              },
            ]}
          />
        ) : null}
      </Form>
    </Modal>
  );
}
