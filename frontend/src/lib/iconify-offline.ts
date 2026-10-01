// Configure Iconify to work offline with bundled icon data.
//
// Every collection referenced anywhere in the app (including the extension
// frontends) MUST be registered here. @iconify/svelte silently falls back to
// https://api.iconify.design for an unregistered prefix, which both breaks the
// offline guarantee this file exists for and leaks the user's mail-provider
// choices to a third-party host.
//
// Collections in use:
//   mdi          — all UI icons
//   lucide       — a few UI icons
//   logos        — provider marks: logos:{google-gmail,microsoft-icon,yahoo,google-icon}
//                  used by lib/email/providers.ts, AccountForm.svelte and the
//                  calendar extension's AddCalendarMenu.svelte
//   simple-icons — provider marks: simple-icons:{icloud,protonmail,fastmail,zoho,aol}
//                  used by lib/email/providers.ts
import { addCollection } from '@iconify/svelte'
import mdiIcons from '@iconify-json/mdi/icons.json'
import lucideIcons from '@iconify-json/lucide/icons.json'
import logosIcons from '@iconify-json/logos/icons.json'
import simpleIcons from '@iconify-json/simple-icons/icons.json'

addCollection(mdiIcons)
addCollection(lucideIcons)
addCollection(logosIcons)
addCollection(simpleIcons)
