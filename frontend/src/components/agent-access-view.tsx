'use client';
import { useCallback, useEffect, useState } from 'react';
import { ConfirmForm } from '@/components/agent-access/confirm-form';
import { AgentSummary } from '@/components/agent-access/agent-summary';
import {
  CheckingScreen,
  DeclinedScreen,
  DoneScreen,
  FailScreen,
  InvalidScreen,
  MissingScreen,
  OtherUserScreen,
  SignInScreen,
} from '@/components/agent-access/screens';
import { useAuth } from '@/components/auth-provider';
import { AuthShell } from '@/components/auth-shell';
import { useAsync } from '@/hooks';
import { useTranslations } from '@/i18n/use-translations';
import { agentAccessStage } from '@/lib/agent-access';
import { ApiError, api } from '@/lib/api';
import { takeHashToken } from '@/lib/hash-token';

type Outcome = { kind: 'done'; created: boolean } | { kind: 'declined' } | { kind: 'invalid' };

/**
 * `/agent-access#token=…`: the owner confirms or refuses the key an AI agent got from `sign_up` (D-025). The preview says
 * whether the address has an account; together with who is signed in here that picks the screen (see agentAccessStage).
 * The preview spends nothing, so reopening the page after signing in is safe.
 */
export function AgentAccessView() {
  const t = useTranslations('agentAccess');
  const { user, loading: authLoading, error: authError, retry } = useAuth();
  // undefined until the fragment was read on the client; null when there is none.
  const [token, setToken] = useState<string | null | undefined>(undefined);
  const [outcome, setOutcome] = useState<Outcome | null>(null);
  useEffect(() => setToken(takeHashToken()), []);

  const load = useCallback(() => (token ? api.auth.agentSignupPreview(token) : Promise.resolve(null)), [token]);
  const { data: preview, error, loading, reload } = useAsync(load);

  const declined = useCallback(() => setOutcome({ kind: 'declined' }), []);
  const invalid = useCallback(() => setOutcome({ kind: 'invalid' }), []);

  if (outcome?.kind === 'declined') return <DeclinedScreen />;
  if (outcome?.kind === 'invalid') return <InvalidScreen />;
  if (outcome?.kind === 'done') return <DoneScreen agentName={preview?.agentName ?? ''} created={outcome.created} />;
  if (token === null) return <MissingScreen />;
  // 400: unknown, used, refused or expired. Anything else (offline, 5xx) may pass on a retry.
  if (error instanceof ApiError && error.status === 400) return <InvalidScreen />;
  if (error) return <FailScreen error={error} onRetry={reload} />;
  if (authError && !user && !authLoading) return <FailScreen error={authError} onRetry={retry} />;
  if (token === undefined || loading || authLoading || !preview) return <CheckingScreen />;

  const stage = agentAccessStage(preview, user);
  if (stage === 'otherUser') return <OtherUserScreen preview={preview} current={user?.email ?? ''} />;
  if (stage === 'signIn') return <SignInScreen preview={preview} token={token} onDeclined={declined} onInvalid={invalid} />;
  const create = stage === 'create';
  const intro = create ? t('introCreate', { agent: preview.agentName, email: preview.email }) : t('introExisting', { agent: preview.agentName, email: preview.email });
  return (
    <AuthShell title={t('title')} description={intro}>
      <div className="space-y-6">
        <AgentSummary preview={preview} />
        <ConfirmForm token={token} create={create} onConfirmed={(created) => setOutcome({ kind: 'done', created })} onDeclined={declined} onInvalid={invalid} />
      </div>
    </AuthShell>
  );
}
