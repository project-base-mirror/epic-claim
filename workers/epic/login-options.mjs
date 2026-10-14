// Validate before importing Playwright, acquiring locks or opening a browser.
export function loginOptions(args) {
  if (args.length < 1 || args.length > 2 || !/^[1-9][0-9]*$/.test(args[0])) throw Error('Invalid account ID');
  const accountID = Number(args[0]);
  if (!Number.isSafeInteger(accountID)) throw Error('Invalid account ID');
  const url = new URL(args[1] || 'https://store.epicgames.com/en-US/');
  if (url.protocol !== 'https:' || url.hostname !== 'store.epicgames.com' || url.port || url.username || url.password) throw Error('Invalid destination');
  return { accountID, destination: url.href };
}
