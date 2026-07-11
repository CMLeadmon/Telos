import { test, expect } from '@playwright/test';
import * as fs from 'fs';
import * as path from 'path';

test.describe('Telos Full End-to-End Frontend Functionality', () => {
  const screenshotDir = path.join(__dirname, '..', 'test-results', 'screenshots');

  test.beforeAll(() => {
    if (!fs.existsSync(screenshotDir)) {
      fs.mkdirSync(screenshotDir, { recursive: true });
    }
  });

  test('Test Chat message submission and bot auto-reply', async ({ page }) => {
    // Conditionally proxy WebSocket to force chat to fail while keeping HMR/other WS functional
    await page.addInitScript(() => {
      const OriginalWebSocket = window.WebSocket;
      class MockWebSocket extends OriginalWebSocket {
        constructor(url: string, protocols?: string | string[]) {
          if (url.includes('/api/v1/chat/ws')) {
            super('ws://localhost:9999/invalid');
          } else {
            super(url, protocols);
          }
        }
      }
      window.WebSocket = MockWebSocket as unknown as typeof WebSocket;
    });

    // Navigate to the Home page
    await page.goto('/');
    await page.waitForLoadState('networkidle');

    // Verify chat message input is visible
    const chatInput = page.locator('input[placeholder^="Message #"]');
    await expect(chatInput).toBeVisible();

    // Type and send a message
    const testMessage = 'Hello, this is an automated UI verification test!';
    await chatInput.fill(testMessage);
    await chatInput.press('Enter');

    // Verify that our message was appended to the chat list
    const sentMessageLocator = page.locator(`p:has-text("${testMessage}")`);
    await expect(sentMessageLocator.first()).toBeVisible();

    // Wait for the local bot echo reply simulator to trigger (1 second delay in code)
    const botMessageLocator = page.locator('p:has-text("echo: Hello, this is an automated UI verification test!")');
    await expect(botMessageLocator.first()).toBeVisible({ timeout: 3000 });

    // Capture visual layout of the chat exchange
    await page.screenshot({ path: path.join(screenshotDir, 'chat_functionality_success.png') });
  });

  test('Test Voice signaling token request', async ({ page }) => {
    await page.goto('/');
    await page.waitForLoadState('networkidle');

    // Intercept the backend voice token endpoint
    let tokenRequestIntercepted = false;
    let roomParam = '';
    let userParam = '';

    await page.route('**/api/v1/voice/token*', async (route) => {
      tokenRequestIntercepted = true;
      const url = new URL(route.request().url());
      roomParam = url.searchParams.get('room') || '';
      userParam = url.searchParams.get('user') || '';

      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ token: 'mock-jwt-voice-token' }),
      });
    });

    // Locate and click the "voice-lounge" channel button to join
    const voiceLoungeButton = page.locator('button:has-text("voice-lounge")');
    await expect(voiceLoungeButton).toBeVisible();
    await voiceLoungeButton.click();

    // Verify that the frontend made a GET request to our gateway voice token API with proper parameters
    await expect.poll(() => tokenRequestIntercepted).toBe(true);
    expect(roomParam).toBe('voice-lounge');
    expect(userParam).toBe('cleadmon');

    await page.screenshot({ path: path.join(screenshotDir, 'voice_channel_joining_state.png') });
  });

  test('Test Media Stream library, items, and HLS player load', async ({ page }) => {
    // Force fallback to native HTML5 video player by deleting MediaSource support in test context,
    // which makes hls.js fall back to native video element playing our MP4 mockup directly.
    await page.addInitScript(() => {
      delete (window as unknown as Record<string, unknown>).MediaSource;
    });

    // Intercept media library listings and items
    await page.route('**/api/v1/media', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify([
          { id: 'movies', name: 'Movies', type: 'video' },
          { id: 'audiobooks', name: 'Audiobooks', type: 'audio' },
        ]),
      });
    });

    await page.route('**/api/v1/media/items?parentId=movies', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify([
          { id: 'raising-helen', title: 'Raising Helen (2004)', duration: '1h 59m', type: 'Movie' },
          { id: 'code-sovereignty', title: 'Sovereignty of Code', duration: '1h 45m', type: 'Movie' },
        ]),
      });
    });

    // Intercept stream route and fulfill with a valid local MP4 test file
    await page.route('**/api/v1/stream/video/raising-helen', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'video/mp4',
        path: path.join(__dirname, 'fixtures', 'test.mp4'),
      });
    });

    await page.goto('/');
    await page.waitForLoadState('networkidle');

    // 1. Switch to the Stream module
    const streamNavButton = page.locator('nav button[aria-label="Stream"]');
    await streamNavButton.click();

    // 2. Verify libraries fetched and Movies button is visible
    const moviesLibButton = page.locator('button:has-text("Movies")');
    await expect(moviesLibButton).toBeVisible();
    await moviesLibButton.click();

    // 3. Verify item list loads
    const movieItem = page.locator('span:has-text("Raising Helen (2004)")');
    await expect(movieItem).toBeVisible();

    // 4. Click play on the first item
    await movieItem.click();

    // 5. Verify the streaming player container opens and shows the title
    const playerHeader = page.locator('h2:has-text("Streaming: Raising Helen (2004)")');
    await expect(playerHeader).toBeVisible();

    // Verify video tag exists
    const videoTag = page.locator('video');
    await expect(videoTag).toBeVisible();

    // 6. Wait for the video to start playing (currentTime > 0.1s)
    await page.waitForFunction(() => {
      const video = document.querySelector('video');
      return video && !video.paused && video.currentTime > 0.1;
    }, { timeout: 15000 });

    // Take screenshot of the active media stream layout while the movie is playing
    await page.screenshot({ path: path.join(screenshotDir, 'media_stream_player_active.png') });
  });
});
