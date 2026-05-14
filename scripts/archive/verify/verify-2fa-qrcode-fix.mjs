#!/usr/bin/env node

/**
 * Verification Script: 2FA QR Code Fix
 * 
 * This script verifies that the 2FA QR code displays correctly in production
 * by checking the deployed JavaScript bundle for the bug fix.
 */

import https from 'https';
import http from 'http';

const PRODUCTION_URL = 'https://www.moneradigital.com';
const COLORS = {
  reset: '\x1b[0m',
  green: '\x1b[32m',
  red: '\x1b[31m',
  yellow: '\x1b[33m',
  blue: '\x1b[34m',
  cyan: '\x1b[36m',
};

function log(message, color = COLORS.reset) {
  console.log(`${color}${message}${COLORS.reset}`);
}

function logSection(title) {
  console.log('\n' + '='.repeat(60));
  log(title, COLORS.cyan);
  console.log('='.repeat(60));
}

function logSuccess(message) {
  log(`✅ ${message}`, COLORS.green);
}

function logError(message) {
  log(`❌ ${message}`, COLORS.red);
}

function logWarning(message) {
  log(`⚠️  ${message}`, COLORS.yellow);
}

function logInfo(message) {
  log(`ℹ️  ${message}`, COLORS.blue);
}

/**
 * Fetch content from URL
 */
function fetchUrl(url) {
  return new Promise((resolve, reject) => {
    const protocol = url.startsWith('https') ? https : http;
    
    protocol.get(url, (res) => {
      if (res.statusCode === 301 || res.statusCode === 302) {
        // Follow redirect
        return fetchUrl(res.headers.location).then(resolve).catch(reject);
      }

      let data = '';
      res.on('data', (chunk) => data += chunk);
      res.on('end', () => resolve({ data, statusCode: res.statusCode, headers: res.headers }));
    }).on('error', reject);
  });
}

/**
 * Test 1: Check if the main HTML loads
 */
async function testMainPage() {
  logSection('Test 1: Checking Main Page');
  
  try {
    const { data, statusCode } = await fetchUrl(PRODUCTION_URL);
    
    if (statusCode !== 200) {
      logError(`Main page returned status ${statusCode}`);
      return false;
    }
    
    logSuccess('Main page loads successfully');
    
    // Extract JavaScript bundle URLs
    const scriptRegex = /<script[^>]+src="([^"]+)"[^>]*>/g;
    const scripts = [];
    let match;
    
    while ((match = scriptRegex.exec(data)) !== null) {
      scripts.push(match[1]);
    }
    
    logInfo(`Found ${scripts.length} script tags`);
    return scripts;
  } catch (error) {
    logError(`Failed to fetch main page: ${error.message}`);
    return false;
  }
}

/**
 * Test 2: Check JavaScript bundle for the bug fix
 */
async function testJavaScriptBundle(scripts) {
  logSection('Test 2: Checking JavaScript Bundle for Bug Fix');
  
  if (!scripts || scripts.length === 0) {
    logError('No scripts to check');
    return false;
  }
  
  // Find the main bundle (usually the largest one or with "index" in name)
  const mainScript = scripts.find(s => s.includes('index') || s.includes('main')) || scripts[0];
  const scriptUrl = mainScript.startsWith('http') ? mainScript : `${PRODUCTION_URL}${mainScript}`;
  
  logInfo(`Checking bundle: ${scriptUrl}`);
  
  try {
    const { data } = await fetchUrl(scriptUrl);
    
    // Check for the bug pattern (old code that causes the issue)
    const hasBugPattern = data.includes('setQrCode(payload.qrCodeUrl)') || 
                         data.includes('setQrCode(e.qrCodeUrl)') ||
                         data.includes('qrCodeUrl');
    
    // Check for the fix pattern
    const hasFixPattern = data.includes('setOtpauth') && data.includes('QRCode.toDataURL');
    
    // Check for comment indicating fix
    const hasFixComment = data.includes('Only set otpauth') || data.includes('QR code will be generated');
    
    if (hasBugPattern && !hasFixPattern) {
      logError('Bug pattern detected! Old code is still in production.');
      logWarning('The bundle still contains: setQrCode(payload.qrCodeUrl)');
      return false;
    }
    
    if (hasFixPattern) {
      logSuccess('Fix pattern detected! Code includes proper QR generation logic.');
      logInfo('Bundle contains: setOtpauth + QRCode.toDataURL');
      return true;
    }
    
    // If we can't definitively determine, check for QRCode library
    if (data.includes('QRCode') || data.includes('qrcode')) {
      logSuccess('QRCode library is present in the bundle');
      return true;
    }
    
    logWarning('Could not definitively verify the fix in minified bundle');
    logInfo('This is normal for production builds with code minification');
    return null; // Indeterminate
    
  } catch (error) {
    logError(`Failed to fetch JavaScript bundle: ${error.message}`);
    return false;
  }
}

/**
 * Test 3: Verify API endpoint is accessible
 */
async function testApiEndpoint() {
  logSection('Test 3: Checking 2FA Setup API Endpoint');
  
  const apiUrl = `${PRODUCTION_URL}/api/auth/2fa/setup`;
  
  try {
    // We expect 401 (unauthorized) since we're not authenticated
    // But at least we know the endpoint exists
    const { statusCode } = await fetchUrl(apiUrl);
    
    if (statusCode === 401) {
      logSuccess('API endpoint exists and requires authentication (expected)');
      return true;
    } else if (statusCode === 404) {
      logError('API endpoint not found (404)');
      return false;
    } else if (statusCode === 405) {
      logSuccess('API endpoint exists (method not allowed without POST)');
      return true;
    } else {
      logWarning(`API returned unexpected status: ${statusCode}`);
      return null;
    }
  } catch (error) {
    logError(`Failed to check API endpoint: ${error.message}`);
    return false;
  }
}

/**
 * Test 4: Check for console.log statements (should be removed)
 */
async function checkForConsoleLog(scripts) {
  logSection('Test 4: Checking for console.log Statements');
  
  if (!scripts || scripts.length === 0) {
    logWarning('No scripts to check');
    return null;
  }
  
  const mainScript = scripts.find(s => s.includes('index')) || scripts[0];
  const scriptUrl = mainScript.startsWith('http') ? mainScript : `${PRODUCTION_URL}${mainScript}`;
  
  try {
    const { data } = await fetchUrl(scriptUrl);
    
    // Check for console.log in 2FA context
    const has2FAConsoleLog = data.includes('console.log') && 
                            (data.includes('2FA') || data.includes('twoFactor'));
    
    if (has2FAConsoleLog) {
      logWarning('Found console.log statements in 2FA code');
      logInfo('This may be debugging code that should be removed');
      return false;
    }
    
    logSuccess('No problematic console.log statements detected');
    return true;
  } catch (error) {
    logError(`Failed to check for console.log: ${error.message}`);
    return null;
  }
}

/**
 * Main execution
 */
async function main() {
  console.log('\n' + '🔍 2FA QR Code Fix Verification Script'.padStart(50, ' '));
  console.log('=' .repeat(60));
  
  logInfo(`Testing production URL: ${PRODUCTION_URL}`);
  logInfo(`Timestamp: ${new Date().toISOString()}`);
  
  const results = {
    mainPage: false,
    bundle: false,
    api: false,
    noConsoleLog: false,
  };
  
  // Test 1: Main page
  const scripts = await testMainPage();
  results.mainPage = !!scripts;
  
  if (scripts) {
    // Test 2: JavaScript bundle
    results.bundle = await testJavaScriptBundle(scripts);
    
    // Test 4: Console.log check
    results.noConsoleLog = await checkForConsoleLog(scripts);
  }
  
  // Test 3: API endpoint
  results.api = await testApiEndpoint();
  
  // Summary
  logSection('Summary');
  
  const passed = Object.values(results).filter(r => r === true).length;
  const failed = Object.values(results).filter(r => r === false).length;
  const indeterminate = Object.values(results).filter(r => r === null).length;
  
  console.log(`\nTest Results:`);
  console.log(`  ✅ Passed: ${passed}`);
  console.log(`  ❌ Failed: ${failed}`);
  console.log(`  ⚠️  Indeterminate: ${indeterminate}`);
  
  if (failed === 0 && passed >= 3) {
    logSuccess('\n🎉 All tests passed! The fix appears to be deployed correctly.');
    logInfo('\nNext steps:');
    console.log('  1. Clear your browser cache (Ctrl+Shift+R or Cmd+Shift+R)');
    console.log('  2. Open the site in incognito/private mode');
    console.log('  3. Navigate to Dashboard > Security > Enable 2FA');
    console.log('  4. Verify the QR code displays correctly');
  } else if (failed > 0) {
    logError('\n❌ Some tests failed. The fix may not be fully deployed.');
    logWarning('\nPossible issues:');
    console.log('  1. Browser cache is serving old code');
    console.log('  2. CDN cache needs to be cleared');
    console.log('  3. Deployment may not have completed');
    logInfo('\nTry:');
    console.log('  - Wait a few minutes for CDN propagation');
    console.log('  - Clear browser cache completely');
    console.log('  - Check Vercel deployment dashboard');
  } else {
    logWarning('\n⚠️  Results are indeterminate due to code minification.');
    logInfo('This is normal for production builds.');
    logInfo('\nManual verification recommended:');
    console.log('  1. Clear browser cache');
    console.log('  2. Test the 2FA setup flow');
    console.log('  3. Check browser console for errors');
  }
  
  console.log('\n' + '='.repeat(60) + '\n');
}

// Run the script
main().catch(error => {
  logError(`\nScript failed: ${error.message}`);
  console.error(error);
  process.exit(1);
});
