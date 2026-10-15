'use client';
import { Notice } from '@/components/states';
import { Badge } from '@/components/ui/badge';
import { Checkbox } from '@/components/ui/checkbox';
import { useTranslations } from '@/i18n/use-translations';
import type { ApiKey } from '@/lib/types';

interface Props {
  trusted: boolean;
  confirmed: boolean;
  onTrusted: (v: boolean) => void;
  onConfirmed: (v: boolean) => void;
}

/**
 * Opt-out of the owner's approval for this key (dangerous_policy "trusted", D-013). Off by default, and turning it on
 * needs a second, explicit confirmation: after that the key can publish, delete and disconnect without asking.
 */
export function TrustedPolicyField({ trusted, confirmed, onTrusted, onConfirmed }: Props) {
  const t = useTranslations('developer.trusted');
  return (
    <div className="space-y-3">
      <div className="flex items-start gap-2.5">
        <Checkbox id="key-trusted" checked={trusted} onCheckedChange={(c) => onTrusted(c === true)} />
        <label htmlFor="key-trusted" className="text-sm">
          <span className="font-medium">{t('label')}</span>
          <span className="block text-muted-foreground">
            {t('hint')}
          </span>
        </label>
      </div>
      {trusted ? (
        <>
          <Notice tone="danger">
            {t('warning')}
          </Notice>
          <div className="flex items-start gap-2.5">
            <Checkbox id="key-trusted-ack" checked={confirmed} onCheckedChange={(c) => onConfirmed(c === true)} />
            <label htmlFor="key-trusted-ack" className="text-sm">
              {t('ack')}
            </label>
          </div>
        </>
      ) : null}
    </div>
  );
}

/**
 * How a key handles dangerous actions, in the key list. `trusted_connect` keys come from an agent sign-up (D-025): only
 * connecting a network skips the approval, so they read differently from a fully trusted key.
 */
export function PolicyBadge({ policy }: { policy: ApiKey['dangerous_policy'] }) {
  const t = useTranslations('developer.apiKeys');
  if (policy === 'trusted') return <Badge tone="warning" className="mt-1.5 flex w-fit">{t('badgeTrusted')}</Badge>;
  if (policy === 'trusted_connect') return <Badge tone="info" className="mt-1.5 flex w-fit">{t('badgeTrustedConnect')}</Badge>;
  return <Badge tone="neutral" className="mt-1.5 flex w-fit">{t('badgeAsks')}</Badge>;
}
