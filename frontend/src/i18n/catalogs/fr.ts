import type { Catalog } from '@/i18n/pseudo';
import nav from '../../../messages/fr/nav.json';
import language from '../../../messages/fr/language.json';
import common from '../../../messages/fr/common.json';
import errors from '../../../messages/fr/errors.json';
import shell from '../../../messages/fr/shell.json';
import legal from '../../../messages/fr/legal.json';
import auth from '../../../messages/fr/auth.json';
import posts from '../../../messages/fr/posts.json';
import settings from '../../../messages/fr/settings.json';
import dashboard from '../../../messages/fr/dashboard.json';
import accounts from '../../../messages/fr/accounts.json';
import analytics from '../../../messages/fr/analytics.json';
import approvals from '../../../messages/fr/approvals.json';
import calendar from '../../../messages/fr/calendar.json';
import composer from '../../../messages/fr/composer.json';
import media from '../../../messages/fr/media.json';
import developer from '../../../messages/fr/developer.json';
import agentAccess from '../../../messages/fr/agentAccess.json';

/** fr, assembled from messages/fr/*.json. One module per locale, so the build emits one lazy chunk per locale. `npm run i18n:check` keeps this list equal to the files on disk. */
const catalog: Catalog = { nav, language, common, errors, shell, legal, auth, posts, settings, dashboard, accounts, analytics, approvals, calendar, composer, media, developer, agentAccess };

export default catalog;
