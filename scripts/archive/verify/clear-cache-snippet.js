// Copy and paste this into your browser's DevTools Console (F12)
// This will clear the cache and reload the page

(async function clearCacheAndReload() {
  console.log('🧹 Clearing browser cache...');
  
  // Clear Cache Storage
  if ('caches' in window) {
    const cacheNames = await caches.keys();
    console.log(`Found ${cacheNames.length} caches to clear`);
    
    for (const name of cacheNames) {
      await caches.delete(name);
      console.log(`✅ Cleared cache: ${name}`);
    }
  }
  
  // Clear localStorage
  localStorage.clear();
  console.log('✅ Cleared localStorage');
  
  // Clear sessionStorage
  sessionStorage.clear();
  console.log('✅ Cleared sessionStorage');
  
  console.log('🔄 Reloading page in 2 seconds...');
  
  setTimeout(() => {
    // Force hard reload
    window.location.reload(true);
  }, 2000);
})();
