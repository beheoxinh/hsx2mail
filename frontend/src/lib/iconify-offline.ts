// Configure Iconify to work offline with bundled icon data
import { addCollection } from '@iconify/svelte'

// Import icon data from installed packages
import mdiIcons from '@iconify-json/mdi/icons.json'
import lucideIcons from '@iconify-json/lucide/icons.json'
import heroiconsIcons from '@iconify-json/heroicons/icons.json'
import logosIcons from '@iconify-json/logos/icons.json'
import simpleIcons from '@iconify-json/simple-icons/icons.json'

// Add all icon collections
addCollection(mdiIcons)
addCollection(lucideIcons)
addCollection(heroiconsIcons)
addCollection(logosIcons)
addCollection(simpleIcons)
