'use client';
import { Check, X } from 'lucide-react';
import { Card } from '@/components/ui/card';
import { useFormat } from '@/i18n/use-format';
import { useTranslations } from '@/i18n/use-translations';
import { agentAbilities, agentLimits, type AgentAccessPreview } from '@/lib/agent-access';

/**
 * What confirming hands the agent, derived from the scopes of its key so the list is never more generous than the key:
 * what it can do, what it can never do, and how long the link lasts.
 */
export function AgentSummary({ preview }: { preview: AgentAccessPreview }) {
  const t = useTranslations('agentAccess');
  const fmt = useFormat();
  const can = agentAbilities(preview.scopes);
  const cannot = agentLimits(preview.scopes);
  return (
    <Card as="section" aria-label={preview.agentName} className="space-y-4 text-sm">
      {can.length > 0 ? (
        <div>
          <h2 className="font-medium">{t('canTitle')}</h2>
          <ul className="mt-2 space-y-1.5">
            {can.map((id) => (
              <li key={id} className="flex gap-2">
                <Check aria-hidden className="mt-0.5 h-4 w-4 shrink-0 text-success" />
                <span>{t(`can.${id}`)}</span>
              </li>
            ))}
          </ul>
        </div>
      ) : null}
      {cannot.length > 0 ? (
        <div>
          <h2 className="font-medium">{t('cannotTitle')}</h2>
          <ul className="mt-2 space-y-1.5">
            {cannot.map((id) => (
              <li key={id} className="flex gap-2">
                <X aria-hidden className="mt-0.5 h-4 w-4 shrink-0 text-muted-foreground" />
                <span>{t(`cannot.${id}`)}</span>
              </li>
            ))}
          </ul>
        </div>
      ) : null}
      <div className="space-y-1 text-muted-foreground">
        <p>{t('revokeHint')}</p>
        {preview.expiresAt ? <p>{t('expires', { time: fmt.dateTime(preview.expiresAt) })}</p> : null}
      </div>
    </Card>
  );
}
