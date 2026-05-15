const fs = require('fs');
const path = require('path');

const securityFile = path.join(__dirname, '../src/pages/dashboard/Security.tsx');
const enFile = path.join(__dirname, '../src/i18n/locales/en.json');
const zhFile = path.join(__dirname, '../src/i18n/locales/zh.json');

const securityContent = fs.readFileSync(securityFile, 'utf8');
const enJson = JSON.parse(fs.readFileSync(enFile, 'utf8'));
const zhJson = JSON.parse(fs.readFileSync(zhFile, 'utf8'));

// Regex to find t("key") or t('key')
const regex = /t\s*\(\s*["']([^"']+)["']\s*\)/g;
let match;
const keys = new Set();

while ((match = regex.exec(securityContent)) !== null) {
  keys.add(match[1]);
}

console.log(`Found ${keys.size} translation keys in Security.tsx`);

let hasError = false;

function checkKey(json, lang, key) {
  const parts = key.split('.');
  let current = json;
  for (const part of parts) {
    if (current && current[part] !== undefined) {
      current = current[part];
    } else {
      console.error(`Missing key in ${lang}: ${key}`);
      return false;
    }
  }
  return true;
}

keys.forEach(key => {
  if (!checkKey(enJson, 'en', key)) hasError = true;
  if (!checkKey(zhJson, 'zh', key)) hasError = true;
});

if (hasError) {
  console.error('Validation FAILED');
  process.exit(1);
} else {
  console.log('All keys present in both languages.');
  console.log('Validation PASSED');
}
