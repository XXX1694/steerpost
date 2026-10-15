import type { Catalog } from '@/i18n/pseudo';
import nav from '../../../messages/pt-BR/nav.json';
import language from '../../../messages/pt-BR/language.json';
import common from '../../../messages/pt-BR/common.json';
import errors from '../../../messages/pt-BR/errors.json';
import shell from '../../../messages/pt-BR/shell.json';
import legal from '../../../messages/pt-BR/legal.json';
import auth from '../../../messages/pt-BR/auth.json';
import posts from '../../../messages/pt-BR/posts.json';
import settings from '../../../messages/pt-BR/settings.json';
import dashboard from '../../../messages/pt-BR/dashboard.json';
import accounts from '../../../messages/pt-BR/accounts.json';
import analytics from '../../../messages/pt-BR/analytics.json';
import approvals from '../../../messages/pt-BR/approvals.json';
import calendar from '../../../messages/pt-BR/calendar.json';
import composer from '../../../messages/pt-BR/composer.json';
import media from '../../../messages/pt-BR/media.json';
import developer from '../../../messages/pt-BR/developer.json';
import agentAccess from '../../../messages/pt-BR/agentAccess.json';

/** pt-BR, assembled from messages/pt-BR/*.json. One module per locale, so the build emits one lazy chunk per locale. `npm run i18n:check` keeps this list equal to the files on disk. */
const catalog: Catalog = { nav, language, common, errors, shell, legal, auth, posts, settings, dashboard, accounts, analytics, approvals, calendar, composer, media, developer, agentAccess };

export default catalog;
