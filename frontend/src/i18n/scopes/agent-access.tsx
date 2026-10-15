'use client';
import type { ReactNode } from 'react';
import { MessagesScope } from '@/i18n/scope';
import agentAccess from '../../../messages/en/agentAccess.json';

/** /agent-access, the page an owner opens from the email after an agent signed up (D-025). The English JSON is a module-level constant, so the scope never re-registers and ships only with this route's chunk. */
const BUNDLES = { agentAccess };

export function AgentAccessScope({ children }: { children: ReactNode }) {
  return <MessagesScope bundles={BUNDLES}>{children}</MessagesScope>;
}
