# Findings - Vercel Deployment

## Project Configuration
- **Root Directory**: `/Users/eric/dreame/code/MoneraDigital`
- **Deployment Script**: `scripts/deploy.sh`
- **Frontend Tech Stack**: React, TypeScript, Vite, Tailwind CSS

## Vercel Details
- Expected command: `vercel --prod --yes`
- **Vercel CLI Version**: 50.3.2
- **Config File**: `vercel.json` found.
- **Rewrites**: `/api/(.*)` -> `https://monera-digital--gyc567.replit.app/api/$1`
- **Build Command**: `npm run build`
- **Output Directory**: `dist`

## Deployment Results
- **Production URL**: `https://monera-digital-8yakghysp-gyc567s-projects.vercel.app`
- **Custom Domain**: `https://www.moneradigital.com`
- **Build Time**: ~29s
- **Issues Resolved**: Fixed duplicate "onInvalid" attribute in `src/pages/Register.tsx`.


