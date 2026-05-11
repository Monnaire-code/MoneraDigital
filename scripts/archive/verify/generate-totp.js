import { authenticator } from 'otplib';

const secret = process.argv[2];
if (!secret) {
  console.error('Usage: node generate-totp.js <secret>');
  process.exit(1);
}

// Ensure secret is clean
const cleanSecret = secret.replace(/\s/g, '');
const token = authenticator.generate(cleanSecret);
console.log(token);
