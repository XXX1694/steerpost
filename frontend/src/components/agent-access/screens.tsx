'use client';
import Link from 'next/link';
import { useState } from 'react';
import { useAuth } from '@/components/auth-provider';
import { AuthShell } from '@/components/auth-shell';
import { ErrorState, InlineError, Notice } from '@/components/states';
import { Button } from '@/components/ui/button';
import { useErrorText } from '@/hooks';
import { useTranslations } from '@/i18n/use-translations';
import type { AgentAccessPreview } from '@/lib/agent-access';
import { withNext } from '@/lib/safe-next';
import { AgentSummary } from './agent-summary';
import { DeclineButton } from './decline-button';

/** Back here after signing in. The token stays in memory (lib/hash-token), so a client-side sign-in keeps the link. */
const SIGN_IN_HREF = withNext('/login', '/agent-access');

export function CheckingScreen() {
  const t = useTranslations('agentAccess');
  return (
    <AuthShell title={t('title')}>
      <p role="status" className="text-sm text-muted-foreground">
        {t('checking')}
      </p>
    </AuthShell>
  );
}

export function FailScreen({ error, onRetry }: { error: unknown; onRetry: () => void }) {
  const t = useTranslations('agentAccess');
  return (
    <AuthShell title={t('failTitle')}>
      <ErrorState error={error} onRetry={onRetry} />
    </AuthShell>
  );
}

/** No token: opened without the mailed link, or a full reload (a provider sign-in) dropped it from memory. */
export function MissingScreen() {
  const t = useTranslations('agentAccess');
  return <AuthShell title={t('missingTitle')} description={t('missingBody')} />;
}

export function InvalidScreen() {
  const t = useTranslations('agentAccess');
  const ta = useTranslations('auth');
  return <AuthShell title={ta('linkInvalidTitle')} description={t('invalidBody')} />;
}

export function DeclinedScreen() {
  const t = useTranslations('agentAccess');
  return <AuthShell title={t('declinedTitle')} description={t('declinedBody')} />;
}

/** After confirming. A new account has no password (D-023), so the next step is to set one while the session is fresh. */
export function DoneScreen({ agentName, created }: { agentName: string; created: boolean }) {
  const t = useTranslations('agentAccess');
  if (created) {
    return (
      <AuthShell title={t('doneTitle')} description={t('doneCreateBody', { agent: agentName })}>
        <div className="space-y-4">
          <Notice tone="info">{t('noPassword')}</Notice>
          <Button asChild className="w-full">
            <Link href="/settings">{t('setPassword')}</Link>
          </Button>
        </div>
      </AuthShell>
    );
  }
  return (
    <AuthShell title={t('doneTitle')} description={t('doneExistingBody', { agent: agentName })}>
      <Button asChild className="w-full">
        <Link href="/developer">{t('toDeveloper')}</Link>
      </Button>
    </AuthShell>
  );
}

interface PromptProps {
  preview: AgentAccessPreview;
  token: string;
  onDeclined: () => void;
  onInvalid: () => void;
}

/** The account exists and nobody is signed in: confirming needs that account's own session; refusing does not. */
export function SignInScreen({ preview, token, onDeclined, onInvalid }: PromptProps) {
  const t = useTranslations('agentAccess');
  return (
    <AuthShell title={t('title')} description={t('introExisting', { agent: preview.agentName, email: preview.email })}>
      <div className="space-y-4">
        <AgentSummary preview={preview} />
        <p className="text-sm">{t('signInBody', { email: preview.email })}</p>
        <Button asChild className="w-full">
          <Link href={SIGN_IN_HREF}>{t('signIn')}</Link>
        </Button>
        <DeclineButton token={token} onDeclined={onDeclined} onInvalid={onInvalid} />
      </div>
    </AuthShell>
  );
}

/** Someone else is signed in here. Signing out lets the page re-decide: sign in as the right account, or create it. */
export function OtherUserScreen({ preview, current }: { preview: AgentAccessPreview; current: string }) {
  const t = useTranslations('agentAccess');
  const errorText = useErrorText();
  const { logout } = useAuth();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function signOut() {
    setBusy(true);
    setError(null);
    try {
      await logout();
    } catch (err) {
      setError(errorText(err));
      setBusy(false);
    }
  }

  return (
    <AuthShell title={t('otherUserTitle')} description={t('otherUserBody', { current, email: preview.email })}>
      <div className="space-y-2">
        <Button className="w-full" loading={busy} onClick={() => void signOut()}>
          {busy ? t('signingOut') : t('signOut')}
        </Button>
        {error ? <InlineError>{error}</InlineError> : null}
      </div>
    </AuthShell>
  );
}
