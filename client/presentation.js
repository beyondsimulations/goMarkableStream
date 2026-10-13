// Presentation mode chrome (?present=...).
//
// At rest the projector shows only the slides and the ink. Moving the pointer
// fades in one small control bar, which fades out again once the pointer rests
// (Periphery's hover toolbar in the BeyondSimulations Family UI). A key-cap
// hint is shown the first time only, and the connection state is a small grey
// label while it resolves itself; only a connection that needs the presenter
// becomes an orange card with one fix ("Try Again").

const PRESENT_TRAY_IDLE_MS = 2500;
const PRESENT_HINT_DELAY_MS = 800;
const PRESENT_HINT_MS = 6000;
const PRESENT_HINT_KEY = 'goMarkableStream_presentHintSeen';

// Label for each ConnectionState; the dot stays grey for all of them, so the
// failure card is the only orange element on screen.
const PRESENT_STATUS_TEXT = {
	connecting: 'Connecting',
	connected: 'Connected',
	paused: 'Paused',
	'rate-limited': 'Another connection has the stream',
	reconnecting: 'Reconnecting',
	disconnected: 'Not connected'
};

let presentTray = null;
let presentTrayVisible = false;
let presentTrayTimer = null;
let presentHintTimer = null;
let presentRetryHandler = null;

function isPresenting() {
	return document.body.classList.contains('presenting');
}

function initPresentationChrome() {
	document.body.classList.add('presenting');

	presentTray = document.getElementById('presentTray');
	document.addEventListener('mousemove', showPresentTray);
	document.addEventListener('touchstart', showPresentTray, { passive: true });
	presentTray.addEventListener('mouseleave', schedulePresentTrayHide);
	presentTray.addEventListener('focusin', showPresentTray);
	// A mouse click leaves no focus in the bar, so a later key press cannot
	// make a button :focus-visible and hold the bar on the projector. Keyboard
	// activation (detail 0) keeps its focus.
	presentTray.addEventListener('click', (e) => {
		const button = e.target.closest('button');
		if (button && e.detail > 0) {
			button.blur();
		}
	});

	// The same toggles as the keys and the sidebar. Laser, trail and rotate
	// live in the sidebar's buttons, so the bar presses those.
	document.getElementById('presentBoard').addEventListener('click', toggleWhiteBoard);
	document.getElementById('presentLayers').addEventListener('click', toggleLayerOrder);
	document.getElementById('presentTouch').addEventListener('click', toggleTouchBlock);
	document.getElementById('presentFullscreen').addEventListener('click', toggleFullscreen);
	for (const [barId, sidebarId] of [['presentLaser', 'laserToggle'], ['presentTrail', 'trailToggle'], ['presentRotate', 'rotate']]) {
		document.getElementById(barId).addEventListener('click', () => {
			document.getElementById(sidebarId).click();
		});
	}
	document.getElementById('presentRetry').addEventListener('click', () => {
		const retry = presentRetryHandler;
		hidePresentationFailure();
		if (retry) {
			retry();
		}
	});

	// Keep the bar in step when the keyboard, a swipe or the sidebar changes
	// a mode.
	document.addEventListener('papermodechange', refreshPresentTray);
	document.addEventListener('fullscreenchange', refreshPresentTray);
	document.addEventListener('presentstatechange', refreshPresentTray);

	refreshPresentTray();
	showPresentKeyHint();
}

function showPresentTray() {
	// While the bar is up, pointer movement only re-arms the hide timer.
	if (!presentTrayVisible) {
		presentTrayVisible = true;
		// Some paths change a mode without an event (saved preferences), so
		// the bar reads the current state each time it appears.
		refreshPresentTray();
		presentTray.classList.add('visible');
		document.body.classList.add('present-controls-visible');
		hidePresentKeyHint();
	}
	schedulePresentTrayHide();
}

function schedulePresentTrayHide() {
	clearTimeout(presentTrayTimer);
	presentTrayTimer = setTimeout(hidePresentTrayIfIdle, PRESENT_TRAY_IDLE_MS);
}

function hidePresentTrayIfIdle() {
	// Stay while the pointer is on the bar or the keyboard is in it, and look
	// again later. A mouse click focuses a button without :focus-visible, so
	// it does not hold the bar on the projector.
	if (presentTray.matches(':hover') || presentTray.querySelector(':focus-visible')) {
		schedulePresentTrayHide();
		return;
	}
	presentTrayVisible = false;
	presentTray.classList.remove('visible');
	document.body.classList.remove('present-controls-visible');
}

function refreshPresentTray() {
	const pressed = (id, on) => document.getElementById(id).setAttribute('aria-pressed', String(on));
	pressed('presentBoard', !paperTransparent);
	pressed('presentLayers', contentLayerOnTop);
	pressed('presentTouch', touchBlockWanted);
	pressed('presentLaser', laserEnabled);
	pressed('presentTrail', trailEnabled);
	pressed('presentRotate', portrait);
	// The tip says what a click does; screen readers keep the fixed name
	// "Full screen" and hear the state from aria-pressed.
	const fullscreen = Boolean(document.fullscreenElement);
	const fullscreenButton = document.getElementById('presentFullscreen');
	fullscreenButton.setAttribute('aria-pressed', String(fullscreen));
	fullscreenButton.dataset.tip = (fullscreen ? 'Leave full screen' : 'Full screen') + ' · F';
}

// Shown on the first presentation only, like Periphery's first-zoom hint.
function showPresentKeyHint() {
	// Blocked storage throws: then the hint simply shows each time.
	try {
		if (localStorage.getItem(PRESENT_HINT_KEY)) {
			return;
		}
	} catch (e) {
		// fall through
	}
	const hint = document.getElementById('presentKeyHint');
	presentHintTimer = setTimeout(() => {
		// Marked as seen once it is on screen, so a hint that an early pointer
		// movement cut short comes back next time.
		savePreference(PRESENT_HINT_KEY, true);
		hint.classList.add('visible');
		presentHintTimer = setTimeout(hidePresentKeyHint, PRESENT_HINT_MS);
	}, PRESENT_HINT_DELAY_MS);
}

function hidePresentKeyHint() {
	clearTimeout(presentHintTimer);
	document.getElementById('presentKeyHint').classList.remove('visible');
}

// Called by updateConnectionStatus for every state change.
function presentationSetStatus(state) {
	const text = PRESENT_STATUS_TEXT[state] || '';
	const trayStatus = document.getElementById('presentTrayStatus');
	trayStatus.querySelector('.present-status-text').textContent = text;
	trayStatus.classList.toggle('done', state === 'connected');

	// The resting label only appears while the connection is not live. It
	// resolves itself, so it is grey; a failure gets the card instead.
	const pill = document.getElementById('presentStatusPill');
	pill.querySelector('.present-status-text').textContent = text;
	pill.classList.toggle('visible', state !== 'connected' && state !== 'disconnected');
}

// The reconnect attempts ran out: one orange card with one fix.
function showPresentationFailure(onRetry) {
	presentRetryHandler = onRetry;
	document.getElementById('presentFailure').classList.add('visible');
}

function hidePresentationFailure() {
	presentRetryHandler = null;
	document.getElementById('presentFailure').classList.remove('visible');
}
