import { EditOutlined, ReloadOutlined, SaveOutlined } from '@ant-design/icons';
import {
  getRequestStatus,
  type InitialState,
  PageContainer,
  PageEmpty,
  PageError,
  PageForbidden,
  PageLoading,
} from '@mss-boot-io/admin-web/runtime';
import { useIntl, useModel } from '@umijs/max';
import {
  Alert,
  App,
  Button,
  Card,
  Col,
  Descriptions,
  Form,
  Input,
  Row,
  Space,
  Typography,
} from 'antd';
import type { ReactNode } from 'react';
import { useCallback, useEffect, useMemo, useState } from 'react';
import { mallGeneralSettingsAPI } from './api';
import {
  canUpdateMallGeneralSettings,
  emptyMallGeneralSettings,
  getMallGeneralSettingsCapabilities,
  isMallGeneralSettingsEmpty,
  mallGeneralSettingsFields,
  mallGeneralSettingsInput,
  utf8ByteLength,
} from './contract';
import { localizeMallSettingsError } from './errors';
import type { MallGeneralSettings, MallGeneralSettingsInput } from './types';

const localePrefix = 'mallSettings.general';

function readableValue(value: string): ReactNode {
  return value ? value : <Typography.Text type="secondary">—</Typography.Text>;
}

function MallGeneralSettingsPage() {
  const intl = useIntl();
  const { message } = App.useApp();
  const { initialState } = useModel('@@initialState') as {
    initialState?: InitialState;
  };
  const capabilities = useMemo(
    () => getMallGeneralSettingsCapabilities(initialState?.currentUser),
    [initialState?.currentUser],
  );
  const [form] = Form.useForm<MallGeneralSettingsInput>();
  const [settings, setSettings] = useState<MallGeneralSettings>();
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState<unknown>();
  const [editing, setEditing] = useState(false);
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<unknown>();
  const [saved, setSaved] = useState(false);

  const format = useCallback(
    (id: string, defaultMessage: string) => intl.formatMessage({ id, defaultMessage }),
    [intl],
  );

  const requestErrorMessage = useCallback(
    (error: unknown) =>
      localizeMallSettingsError(
        error,
        (id, parameters) => {
          const translated = parameters
            ? intl.formatMessage(
                { id },
                { field: parameters.field ?? '', rule: parameters.rule ?? '' },
              )
            : intl.formatMessage({ id });
          return translated === id ? undefined : translated;
        },
        format(`${localePrefix}.states.requestFailed`, 'The request could not be completed.'),
      ),
    [format, intl],
  );

  const load = useCallback(async () => {
    if (!capabilities.canRead) return;
    setLoading(true);
    setLoadError(undefined);
    setSaved(false);
    try {
      const result = await mallGeneralSettingsAPI.load();
      setSettings(result);
      form.setFieldsValue(mallGeneralSettingsInput(result));
      if (!result.operations.update) {
        setEditing(false);
        setSaveError(undefined);
      }
    } catch (error) {
      setLoadError(error);
    } finally {
      setLoading(false);
    }
  }, [capabilities.canRead, form]);

  useEffect(() => {
    void load();
  }, [load]);

  const beginEditing = useCallback(() => {
    if (!settings?.operations.update) return;
    form.setFieldsValue(mallGeneralSettingsInput(settings));
    setSaveError(undefined);
    setSaved(false);
    setEditing(true);
  }, [form, settings]);

  const cancelEditing = useCallback(() => {
    form.setFieldsValue(settings ? mallGeneralSettingsInput(settings) : emptyMallGeneralSettings());
    setSaveError(undefined);
    setEditing(false);
  }, [form, settings]);

  const save = useCallback(
    async (values: MallGeneralSettingsInput) => {
      if (!capabilities.canUpdate || !settings?.operations.update) return;
      const input = mallGeneralSettingsInput(values);
      setSaving(true);
      setSaveError(undefined);
      setSaved(false);
      try {
        const persisted = await mallGeneralSettingsAPI.update(input);
        setSettings(persisted);
        setLoadError(undefined);
        form.setFieldsValue(mallGeneralSettingsInput(persisted));
        setEditing(false);
        setSaved(true);
        void message.success(format(`${localePrefix}.feedback.saved`, 'General settings saved.'));
      } catch (error) {
        setSaveError(error);
      } finally {
        setSaving(false);
      }
    },
    [capabilities.canUpdate, form, format, message, settings?.operations.update],
  );

  const title = format(`${localePrefix}.title`, 'Mall settings');
  const description = format(
    `${localePrefix}.description`,
    'Manage the safe, tenant-owned general defaults for this mall.',
  );

  if (!capabilities.canRead) {
    return (
      <PageContainer content={description} title={title}>
        <PageForbidden
          message={format(
            `${localePrefix}.states.forbidden`,
            'You do not have permission to view mall settings.',
          )}
        />
      </PageContainer>
    );
  }

  if (getRequestStatus(loadError) === 403) {
    return (
      <PageContainer content={description} title={title}>
        <PageForbidden message={requestErrorMessage(loadError)} />
      </PageContainer>
    );
  }

  if (loading && !settings) {
    return (
      <PageContainer content={description} title={title}>
        <PageLoading rows={6} />
      </PageContainer>
    );
  }

  if (loadError && !settings) {
    return (
      <PageContainer content={description} title={title}>
        <PageError
          message={requestErrorMessage(loadError)}
          onRetry={() => void load()}
          retryLabel={format(`${localePrefix}.actions.retry`, 'Try again')}
          title={format(`${localePrefix}.states.error`, 'Unable to load mall settings')}
        />
      </PageContainer>
    );
  }

  const empty = settings ? isMallGeneralSettingsEmpty(settings) : true;
  const canUpdate = canUpdateMallGeneralSettings(capabilities, settings);
  const descriptionItems = mallGeneralSettingsFields.map((field) => ({
    key: field.name,
    label: format(field.labelMessageId, field.defaultLabel),
    children: readableValue(settings?.[field.name] ?? ''),
  }));

  return (
    <PageContainer content={description} title={title}>
      <Space orientation="vertical" size="large" style={{ width: '100%' }}>
        {saved ? (
          <Alert
            closable
            description={format(
              `${localePrefix}.feedback.savedDescription`,
              'The latest safe general settings are now active for this mall.',
            )}
            onClose={() => setSaved(false)}
            showIcon
            title={format(`${localePrefix}.feedback.saved`, 'General settings saved.')}
            type="success"
          />
        ) : null}

        {loadError && settings ? (
          <Alert
            action={
              <Button
                icon={<ReloadOutlined />}
                loading={loading}
                onClick={() => void load()}
                size="small"
              >
                {format(`${localePrefix}.actions.retry`, 'Try again')}
              </Button>
            }
            description={requestErrorMessage(loadError)}
            showIcon
            title={format(
              `${localePrefix}.states.refreshFailed`,
              'The displayed settings could not be refreshed.',
            )}
            type="warning"
          />
        ) : null}

        {settings && !settings.operations.update ? (
          <Alert
            description={format(
              `${localePrefix}.states.readOnlyDescription`,
              'The isolated runtime can safely read this legacy configuration, but updates remain disabled until a reviewed writable cutover.',
            )}
            showIcon
            title={format(`${localePrefix}.states.readOnly`, 'Mall settings are read-only')}
            type="info"
          />
        ) : null}

        {editing ? (
          <Card
            title={format(`${localePrefix}.editor.title`, 'Edit general settings')}
            extra={
              <Button icon={<ReloadOutlined />} loading={loading} onClick={() => void load()}>
                {format(`${localePrefix}.actions.refresh`, 'Refresh')}
              </Button>
            }
          >
            {saveError ? (
              <Alert
                description={requestErrorMessage(saveError)}
                showIcon
                style={{ marginBottom: 24 }}
                title={
                  getRequestStatus(saveError) === 403
                    ? format(
                        `${localePrefix}.states.updateForbidden`,
                        'You no longer have permission to update these settings.',
                      )
                    : format(`${localePrefix}.states.saveError`, 'Unable to save settings')
                }
                type="error"
              />
            ) : null}
            <Form<MallGeneralSettingsInput>
              form={form}
              layout="vertical"
              name="mall-general-settings-editor"
              onFinish={(values) => void save(values)}
              requiredMark="optional"
            >
              <Row gutter={[24, 0]}>
                {mallGeneralSettingsFields.map((field) => {
                  const label = format(field.labelMessageId, field.defaultLabel);
                  return (
                    <Col key={field.name} xs={24} lg={12}>
                      <Form.Item
                        extra={format(field.helpMessageId, field.defaultHelp)}
                        label={label}
                        name={field.name}
                        rules={[
                          {
                            validator: (_rule: unknown, value: unknown) => {
                              if (typeof value !== 'string') {
                                return Promise.reject(
                                  new Error(
                                    format(
                                      `${localePrefix}.validation.string`,
                                      `${label} must be text.`,
                                    ),
                                  ),
                                );
                              }
                              if (utf8ByteLength(value) > field.maxBytes) {
                                return Promise.reject(
                                  new Error(
                                    intl.formatMessage(
                                      {
                                        id: `${localePrefix}.validation.maxBytes`,
                                        defaultMessage: `${label} must be {max} UTF-8 bytes or fewer.`,
                                      },
                                      { field: label, max: field.maxBytes },
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
                          autoComplete={field.autoComplete}
                          maxLength={field.maxBytes}
                          placeholder={format(field.placeholderMessageId, field.defaultPlaceholder)}
                          type={field.inputType}
                        />
                      </Form.Item>
                    </Col>
                  );
                })}
              </Row>
              <Space>
                <Button htmlType="submit" icon={<SaveOutlined />} loading={saving} type="primary">
                  {format(`${localePrefix}.actions.save`, 'Save')}
                </Button>
                <Button disabled={saving} onClick={cancelEditing}>
                  {format(`${localePrefix}.actions.cancel`, 'Cancel')}
                </Button>
              </Space>
            </Form>
          </Card>
        ) : empty ? (
          <Card>
            <Space orientation="vertical" size="middle" style={{ width: '100%' }}>
              <PageEmpty
                description={format(
                  `${localePrefix}.states.empty`,
                  'No general mall settings have been configured yet.',
                )}
              />
              {canUpdate ? (
                <div style={{ textAlign: 'center' }}>
                  <Button icon={<EditOutlined />} onClick={beginEditing} type="primary">
                    {format(`${localePrefix}.actions.configure`, 'Configure settings')}
                  </Button>
                </div>
              ) : null}
            </Space>
          </Card>
        ) : (
          <Card
            extra={
              <Space>
                <Button icon={<ReloadOutlined />} loading={loading} onClick={() => void load()}>
                  {format(`${localePrefix}.actions.refresh`, 'Refresh')}
                </Button>
                {canUpdate ? (
                  <Button icon={<EditOutlined />} onClick={beginEditing} type="primary">
                    {format(`${localePrefix}.actions.edit`, 'Edit')}
                  </Button>
                ) : null}
              </Space>
            }
            title={format(`${localePrefix}.summary.title`, 'General settings')}
          >
            <Descriptions bordered column={{ xs: 1, md: 2 }} items={descriptionItems} />
          </Card>
        )}
      </Space>
    </PageContainer>
  );
}

export default MallGeneralSettingsPage;
