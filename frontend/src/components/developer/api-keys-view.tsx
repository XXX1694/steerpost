'use client';
import Link from 'next/link';
import { useCallback, useState } from 'react';
import { ConfirmDialog } from '@/components/confirm-dialog';
import { EmptyState, ErrorState, InlineError, LoadingRows } from '@/components/states';
import { useToast } from '@/components/toast';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { CheckboxField } from '@/components/ui/checkbox';
import { Table, Tbody, Td, Th, Thead, Tr } from '@/components/ui/table';
import { Dialog, DialogContent, DialogFooter } from '@/components/ui/dialog';
import { Field, Input, Select } from '@/components/ui/input';
import { api } from '@/lib/api';
import { defaultScopes, hasDangerous, scopeRisk } from '@/lib/scopes';
import { formatRelative } from '@/lib/time';
import type { ApiKey, CreatedApiKey } from '@/lib/types';
import { useAsync, useErrorText } from '@/hooks';
import { nodes } from '@/i18n/rich';
import { CopyButton } from './copy-block';
import { ScopePicker } from './scope-picker';
import { PolicyBadge, TrustedPolicyField } from './trusted-policy';
import { useTranslations } from '@/i18n/use-translations';
import { useFormat } from '@/i18n/use-format';

function RawKeyDialog({ created, onClose }: { created: CreatedApiKey | null; onClose: () => void }) {
  const t = useTranslations();
  return (
    <Dialog open={created !== null} onOpenChange={(o) => !o && onClose()}>
      <DialogContent title={t('developer.apiKeys.rawTitle')} description={t('developer.apiKeys.rawBody')}>
        <div className="space-y-3">
          <code data-testid="raw-key" className="block break-all rounded-md border bg-muted p-3 font-mono text-xs">
            {created?.rawKey}
          </code>
          <CopyButton text={created?.rawKey ?? ''} label={t('developer.apiKeys.copyKey')} />
        </div>
        <DialogFooter>
          <Button onClick={onClose}>{t('common.haveSaved')}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

const EXPIRY = [
  { id: 'never', days: null },
  { id: '30', days: 30 },
  { id: '90', days: 90 },
  { id: 'year', days: 365 },
] as const;

function CreateKeyDialog({ open, onOpenChange, onCreated }: { open: boolean; onOpenChange: (o: boolean) => void; onCreated: (c: CreatedApiKey) => void }) {
  const t = useTranslations();
  const errorText = useErrorText();
  const [name, setName] = useState('');
  const [scopes, setScopes] = useState<string[]>(defaultScopes());
  const [expiry, setExpiry] = useState('90');
  const [ack, setAck] = useState(false);
  const [trusted, setTrusted] = useState(false);
  const [trustAck, setTrustAck] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const dangerous = hasDangerous(scopes);
  const isTrusted = dangerous && trusted; // the choice only exists while a dangerous scope is selected

  async function submit() {
    if (!name.trim()) return setError(t('developer.apiKeys.nameRequired'));
    if (scopes.length === 0) return setError(t('developer.apiKeys.scopesRequired'));
    if (dangerous && !ack) return setError(t('developer.apiKeys.ackRequired'));
    if (isTrusted && !trustAck) return setError(t('developer.apiKeys.trustAckRequired'));
    setBusy(true);
    setError(null);
    try {
      const days = expiry ? Number(expiry) : null;
      const expires_at = days ? new Date(Date.now() + days * 86_400_000).toISOString() : undefined;
      const created = await api.developer.createApiKey({ name: name.trim(), scopes, expires_at, dangerous_policy: isTrusted ? 'trusted' : 'approve' });
      setName('');
      setScopes(defaultScopes());
      setAck(false);
      setTrusted(false);
      setTrustAck(false);
      onOpenChange(false);
      onCreated(created);
    } catch (e) {
      setError(errorText(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent title={t('developer.apiKeys.createTitle')} description={t('developer.apiKeys.createBody')}>
        <div className="space-y-5">
          <Field label={t('developer.apiKeys.nameLabel')}>
            <Input value={name} onChange={(e) => setName(e.target.value)} maxLength={60} placeholder={t('developer.apiKeys.namePlaceholder')} />
          </Field>
          <Field label={t('developer.apiKeys.expiresLabel')}>
            <Select value={expiry} onChange={(e) => setExpiry(e.target.value)}>
              {EXPIRY.map((o) => (
                <option key={o.id} value={o.days ?? ''}>
                  {o.id === 'never' ? t('developer.apiKeys.expiryNever') : o.id === 'year' ? t('developer.apiKeys.expiryYear') : t('developer.apiKeys.expiryDays', { days: o.days })}
                </option>
              ))}
            </Select>
          </Field>
          <ScopePicker value={scopes} onChange={setScopes} />
          {dangerous ? (
            <CheckboxField checked={ack} onCheckedChange={(c) => setAck(c === true)} label={t('developer.apiKeys.ackDangerous')} />
          ) : null}
          {dangerous ? <TrustedPolicyField trusted={trusted} confirmed={trustAck} onTrusted={(v) => { setTrusted(v); if (!v) setTrustAck(false); }} onConfirmed={setTrustAck} /> : null}
          {error ? <InlineError>{error}</InlineError> : null}
        </div>
        <DialogFooter>
          <Button variant="secondary" onClick={() => onOpenChange(false)}>{t('common.cancel')}</Button>
          <Button onClick={() => void submit()} disabled={busy}>{busy ? t('common.creating') : t('developer.apiKeys.createKey')}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function KeyRow({ k, onRevoke }: { k: ApiKey; onRevoke: (k: ApiKey) => void }) {
  const t = useTranslations();
  const fmt = useFormat();
  const expired = k.expires_at !== null && new Date(k.expires_at).getTime() < Date.now();
  return (
    <Tr className="align-top">
      <Td label={t('developer.apiKeys.colName')} className="py-3">
        <div>
          <p className="font-medium">{k.name}</p>
          <code className="text-xs text-muted-foreground">{k.prefix}…</code>
          {k.revoked_at ? null : <PolicyBadge policy={k.dangerous_policy} />}
        </div>
      </Td>
      <Td label={t('developer.apiKeys.colScopes')} className="py-3">
        <div className="flex max-w-xs flex-wrap gap-1">
          {k.scopes.map((s) => (
            <Badge key={s} tone={scopeRisk(s) === 'dangerous' ? 'danger' : scopeRisk(s) === 'medium' ? 'warning' : 'neutral'}>
              {s}
            </Badge>
          ))}
        </div>
      </Td>
      <Td label={t('developer.apiKeys.expiresLabel')} className="py-3 text-muted-foreground md:whitespace-nowrap">{k.expires_at ? <span className="whitespace-nowrap">{fmt.dateTime(k.expires_at)}</span> : t('developer.apiKeys.expiryNever')}</Td>
      <Td label={t('developer.apiKeys.colLastUsed')} className="py-3 text-muted-foreground md:whitespace-nowrap">{formatRelative(k.last_used_at, t)}</Td>
      <Td align="right" className="py-3">
        {k.revoked_at ? <Badge>{t('common.status.account.revoked')}</Badge> : expired ? <Badge tone="warning">{t('developer.apiKeys.expired')}</Badge> : (
          <Button variant="ghost" size="sm" onClick={() => onRevoke(k)} aria-label={t('developer.apiKeys.revokeLabel', { name: k.name })}>{t('developer.apiKeys.revoke')}</Button>
        )}
      </Td>
    </Tr>
  );
}

export function ApiKeysView() {
  const t = useTranslations();
  const load = useCallback(() => api.developer.apiKeys(), []);
  const { data, error, loading, reload } = useAsync(load);
  const toast = useToast();
  const [createOpen, setCreateOpen] = useState(false);
  const [created, setCreated] = useState<CreatedApiKey | null>(null);
  const [revoking, setRevoking] = useState<ApiKey | null>(null);

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between gap-3">
        <p className="text-sm text-muted-foreground">
          {nodes(t.rich('developer.apiKeys.intro', { header: 'Authorization: Bearer sk_live_…', code: (c) => <code className="text-xs">{c}</code> }))}
        </p>
        <Button onClick={() => setCreateOpen(true)}>{t('developer.apiKeys.createKey')}</Button>
      </div>
      {loading && !data ? (
        <LoadingRows rows={2} />
      ) : error || !data ? (
        <ErrorState error={error} onRetry={reload} />
      ) : data.length === 0 ? (
        <EmptyState title={t('developer.apiKeys.emptyTitle')}>
          {nodes(
            t.rich('developer.apiKeys.emptyBody', {
              link: (c) => (
                <Link href="/developer/mcp" className="underline underline-offset-4">
                  {c}
                </Link>
              ),
            }),
          )}
        </EmptyState>
      ) : (
        <Table label={t('developer.apiKeys.tableLabel')}>
          <Thead>
            <Tr>
              <Th>{t('developer.apiKeys.colName')}</Th>
              <Th>{t('developer.apiKeys.colScopes')}</Th>
              <Th>{t('developer.apiKeys.expiresLabel')}</Th>
              <Th>{t('developer.apiKeys.colLastUsed')}</Th>
              <Th><span className="sr-only">{t('developer.apiKeys.colActions')}</span></Th>
            </Tr>
          </Thead>
          <Tbody>
            {data.map((k) => (
              <KeyRow key={k.id} k={k} onRevoke={setRevoking} />
            ))}
          </Tbody>
        </Table>
      )}
      <CreateKeyDialog open={createOpen} onOpenChange={setCreateOpen} onCreated={(c) => { setCreated(c); reload(); }} />
      <RawKeyDialog created={created} onClose={() => setCreated(null)} />
      <ConfirmDialog
        open={revoking !== null}
        onOpenChange={(o) => !o && setRevoking(null)}
        title={t('developer.apiKeys.revokeTitle')}
        description={revoking ? t('developer.apiKeys.revokeBody', { name: revoking.name }) : ''}
        confirmLabel={t('developer.apiKeys.revoke')}
        destructive
        onConfirm={async () => {
          if (!revoking) return;
          await api.developer.revokeApiKey(revoking.id);
          toast.success(t('developer.apiKeys.revoked'));
          reload();
        }}
      />
    </div>
  );
}
