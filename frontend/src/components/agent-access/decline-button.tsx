'use client';
import { useState } from 'react';
import { InlineError } from '@/components/states';
import { Button } from '@/components/ui/button';
import { useErrorText } from '@/hooks';
import { useTranslations } from '@/i18n/use-translations';
import { ApiError, api } from '@/lib/api';
import { forgetHashToken } from '@/lib/hash-token';

interface Props {
  token: string;
  disabled?: boolean;
  onDeclined: () => void;
  /** The link was already used, refused or expired (400). */
  onInvalid: () => void;
}

/** "Not me": refuses the request without signing in, so whoever got the mail by mistake can stop it. The key never works. */
export function DeclineButton({ token, disabled, onDeclined, onInvalid }: Props) {
  const t = useTranslations('agentAccess');
  const errorText = useErrorText();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function decline() {
    setBusy(true);
    setError(null);
    try {
      await api.auth.agentSignupDecline(token);
      forgetHashToken();
      onDeclined();
    } catch (err) {
      setBusy(false);
      if (err instanceof ApiError && err.status === 400) {
        forgetHashToken();
        onInvalid();
      } else setError(errorText(err));
    }
  }

  return (
    <div className="space-y-2">
      <Button type="button" variant="secondary" className="w-full" loading={busy} disabled={disabled} onClick={() => void decline()}>
        {busy ? t('declining') : t('decline')}
      </Button>
      {error ? <InlineError>{error}</InlineError> : null}
    </div>
  );
}
