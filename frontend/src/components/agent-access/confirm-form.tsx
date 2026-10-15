'use client';
import { useState, type FormEvent } from 'react';
import { useAuth } from '@/components/auth-provider';
import { InlineError } from '@/components/states';
import { TermsCheckbox } from '@/components/terms-checkbox';
import { Button } from '@/components/ui/button';
import { Field, Input } from '@/components/ui/input';
import { useErrorText } from '@/hooks';
import { useTranslations } from '@/i18n/use-translations';
import { ApiError, api } from '@/lib/api';
import { forgetHashToken } from '@/lib/hash-token';
import { DeclineButton } from './decline-button';

interface Props {
  token: string;
  /** True when confirming also creates the account: the Terms (D-016) and an optional name are asked for. */
  create: boolean;
  onConfirmed: (accountCreated: boolean) => void;
  onDeclined: () => void;
  onInvalid: () => void;
}

/** Confirm or refuse the agent's request. A refused Terms box or name keeps the link usable; only a 400 without fields ends it. */
export function ConfirmForm({ token, create, onConfirmed, onDeclined, onInvalid }: Props) {
  const t = useTranslations('agentAccess');
  const ta = useTranslations('auth');
  const errorText = useErrorText();
  const { refresh, endSession } = useAuth();
  const [name, setName] = useState('');
  const [accepted, setAccepted] = useState(false);
  const [termsError, setTermsError] = useState(false);
  const [nameError, setNameError] = useState(false);
  const [formError, setFormError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  function refused(err: unknown) {
    if (err instanceof ApiError && err.status === 400 && (err.fields.accept_terms || err.fields.display_name)) {
      setTermsError(Boolean(err.fields.accept_terms));
      setNameError(Boolean(err.fields.display_name));
      setFormError(ta('fixFields'));
    } else if (err instanceof ApiError && err.status === 400) {
      forgetHashToken();
      onInvalid();
    } else if (err instanceof ApiError && err.status === 401) {
      endSession(); // the session ended meanwhile: the page asks to sign in again, the link still works
    } else {
      setFormError(errorText(err));
      // 403/409: another account is signed in (maybe in another tab); re-read it so the page says so.
      if (err instanceof ApiError && (err.status === 403 || err.status === 409)) void refresh();
    }
  }

  async function submit(e: FormEvent) {
    e.preventDefault();
    setFormError(null);
    setNameError(false);
    if (create && !accepted) {
      setTermsError(true);
      setFormError(ta('fixFields'));
      document.getElementById('accept-terms')?.focus();
      return;
    }
    setBusy(true);
    try {
      const displayName = create ? name.trim() : '';
      const res = await api.auth.agentSignupConfirm({ token, accept_terms: create, ...(displayName ? { display_name: displayName } : {}) });
      forgetHashToken();
      onConfirmed(res.accountCreated);
      // A new account was signed in by the answer's cookies; the app learns who it is from /me. After the outcome is
      // set, so the page does not briefly read the new session as "someone else is signed in".
      if (res.accountCreated) void refresh();
    } catch (err) {
      setBusy(false);
      refused(err);
    }
  }

  return (
    <form onSubmit={submit} className="space-y-4" noValidate>
      {create ? (
        <>
          <Field label={ta('name')} htmlFor="name" optional announce={false} error={nameError ? ta('signupComplete.nameTooLong') : undefined}>
            <Input id="name" autoComplete="name" className="max-md:h-11" maxLength={100} value={name} onChange={(e) => setName(e.target.value)} />
          </Field>
          <TermsCheckbox
            checked={accepted}
            onCheckedChange={(v) => {
              setAccepted(v);
              if (v) setTermsError(false);
            }}
            error={termsError ? ta('acceptTerms') : undefined}
          />
        </>
      ) : null}
      {formError ? <InlineError>{formError}</InlineError> : null}
      <Button type="submit" className="w-full" loading={busy}>
        {busy ? t('confirming') : create ? t('confirmCreate') : t('confirm')}
      </Button>
      <DeclineButton token={token} disabled={busy} onDeclined={onDeclined} onInvalid={onInvalid} />
    </form>
  );
}
