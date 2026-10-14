// Present Slides: a popup to paste the address of a reveal.js or Quarto deck,
// with the last few decks one click away. It builds the same ?present=
// address as typing it by hand, so bookmarks keep working.

const RECENT_SLIDES_KEY = 'goMarkableStream_recentSlides';
const RECENT_SLIDES_MAX = 5;
const LOCAL_HOSTS = ['localhost', '127.0.0.1', '[::1]'];

let slidesOpenedByPointer = false;

// What was pasted, as the address to present ({url}), or what is wrong with
// it ({error}). pageProtocol is this page's, 'https:' or 'http:'. Only web
// addresses pass, never javascript: or data: ones.
function normalizeSlidesAddress(text, pageProtocol) {
	let address = text.trim();
	if (!address) {
		return { error: 'Paste the address of the slides.' };
	}
	// An address has no spaces (Chrome would turn one in the host into %20).
	if (/\s/.test(address)) {
		return { error: 'That is not a web address.' };
	}
	// "example.com/talk" means https://example.com/talk; a deck on this
	// computer (a Quarto preview) is usually plain http.
	if (!/^[a-z][a-z0-9+.-]*:\/\//i.test(address)) {
		let host = '';
		try {
			host = new URL('https://' + address).hostname;
		} catch (e) {
			// Reported below.
		}
		address = (LOCAL_HOSTS.includes(host) ? 'http://' : 'https://') + address;
	}
	let url;
	try {
		url = new URL(address);
	} catch (e) {
		return { error: 'That is not a web address.' };
	}
	if (url.protocol !== 'https:' && url.protocol !== 'http:') {
		return { error: 'Use an address that starts with https://.' };
	}
	// The browser shows nothing from http:// inside an https page, except
	// from this computer.
	if (pageProtocol === 'https:' && url.protocol === 'http:' && !LOCAL_HOSTS.includes(url.hostname)) {
		return { error: 'This page uses https, so the browser does not show http:// slides in it. Use the https:// address of the slides.' };
	}
	return { url: url.href };
}

// The deck in the page's address (?present=), checked, or null.
function currentSlidesURL() {
	const presentURL = getQueryParam('present');
	return presentURL ? normalizeSlidesAddress(presentURL, window.location.protocol).url || null : null;
}

// The recent decks live in this browser only. Blocked storage throws: then
// the list is simply empty.
function loadRecentSlides() {
	try {
		const list = JSON.parse(localStorage.getItem(RECENT_SLIDES_KEY) || '[]');
		return Array.isArray(list) ? list.filter((url) => typeof url === 'string').slice(0, RECENT_SLIDES_MAX) : [];
	} catch (e) {
		return [];
	}
}

function saveRecentSlides(list) {
	try {
		localStorage.setItem(RECENT_SLIDES_KEY, JSON.stringify(list));
	} catch (e) {
		// Not remembered; presenting still works.
	}
}

function rememberSlides(url) {
	saveRecentSlides([url, ...loadRecentSlides().filter((other) => other !== url)].slice(0, RECENT_SLIDES_MAX));
}

function forgetSlides(url) {
	saveRecentSlides(loadRecentSlides().filter((other) => other !== url));
}

function presentSlides(url) {
	rememberSlides(url);
	if (isPresenting()) {
		// Another deck in the same mode: swap the slides in place, so the
		// stream, the touch block and the modes stay as they are. The same
		// deck again stays where it is rather than reloading at slide 1.
		if (url !== currentSlidesURL()) {
			const params = new URLSearchParams(window.location.search);
			params.set('present', url);
			history.replaceState(null, '', window.location.pathname + '?' + params.toString());
			iFrame.src = url;
		}
		document.getElementById('slidesDialog').close();
		return;
	}
	const params = new URLSearchParams(window.location.search);
	params.set('present', url);
	window.location.assign(window.location.pathname + '?' + params.toString());
}

function endPresentation() {
	const params = new URLSearchParams(window.location.search);
	params.delete('present');
	params.delete('paper');
	const query = params.toString();
	window.location.assign(window.location.pathname + (query ? '?' + query : ''));
}

function showSlidesError(message) {
	const error = document.getElementById('slidesError');
	error.textContent = message;
	error.hidden = !message;
}

function presentSlidesAddress(text) {
	const result = normalizeSlidesAddress(text, window.location.protocol);
	if (result.error) {
		showSlidesError(result.error);
		document.getElementById('slidesAddress').focus();
		return;
	}
	presentSlides(result.url);
}

// One row per recent deck: its host, then the rest of the address. While
// presenting, the current deck is already in the field and not listed.
function renderRecentSlides() {
	const current = currentSlidesURL();
	const list = loadRecentSlides().filter((url) => url !== current);
	const rows = document.getElementById('slidesRecentList');
	rows.replaceChildren();
	for (const url of list) {
		let host = url;
		let rest = '';
		try {
			const parsed = new URL(url);
			host = parsed.host;
			rest = parsed.pathname + parsed.search + parsed.hash;
			if (rest === '/') {
				rest = '';
			}
		} catch (e) {
			// Shown as stored.
		}

		const open = document.createElement('button');
		open.type = 'button';
		open.className = 'slides-recent-open';
		open.title = url;
		const hostText = document.createElement('span');
		hostText.className = 'slides-recent-host';
		hostText.textContent = host;
		const restText = document.createElement('span');
		restText.className = 'slides-recent-path';
		restText.textContent = rest;
		open.append(hostText, restText);
		open.addEventListener('click', () => {
			// In the field too, so a message about it refers to it.
			document.getElementById('slidesAddress').value = url;
			presentSlidesAddress(url);
		});

		const remove = document.createElement('button');
		remove.type = 'button';
		remove.className = 'slides-recent-remove';
		remove.setAttribute('aria-label', 'Remove ' + host + rest + ' from the recent slides');
		remove.innerHTML = '<svg viewBox="0 0 16 16" width="12" height="12" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" aria-hidden="true"><path d="M4 4l8 8M12 4l-8 8"/></svg>';
		remove.addEventListener('click', () => {
			forgetSlides(url);
			renderRecentSlides();
			document.getElementById('slidesAddress').focus();
		});

		const row = document.createElement('li');
		row.className = 'slides-recent-row';
		row.append(open, remove);
		rows.append(row);
	}
	document.getElementById('slidesRecent').hidden = list.length === 0;
}

// address and error fill the popup from an address that could not be
// presented; otherwise it shows the current deck (or nothing).
function openSlidesDialog(address = null, error = '') {
	const dialog = document.getElementById('slidesDialog');
	if (dialog.open) {
		return;
	}
	const input = document.getElementById('slidesAddress');
	input.value = address !== null ? address : (currentSlidesURL() || '');
	showSlidesError(error);
	renderRecentSlides();
	dialog.showModal();
	// The address is selected, so a paste replaces it.
	input.focus();
	input.select();
}

(function initSlidesDialog() {
	const dialog = document.getElementById('slidesDialog');
	for (const id of ['slidesButton', 'presentSlides']) {
		document.getElementById(id).addEventListener('click', (e) => {
			slidesOpenedByPointer = e.detail > 0;
			openSlidesDialog();
		});
	}
	document.getElementById('slidesForm').addEventListener('submit', (e) => {
		e.preventDefault();
		presentSlidesAddress(document.getElementById('slidesAddress').value);
	});
	document.getElementById('slidesAddress').addEventListener('input', () => showSlidesError(''));
	document.getElementById('slidesCancel').addEventListener('click', () => dialog.close());
	document.getElementById('slidesEnd').addEventListener('click', endPresentation);

	// A click on the dimmed page around the popup closes it. The form fills
	// the dialog, so only the backdrop is the dialog itself; a drag that
	// starts in the field and ends outside does not count.
	let pressedOnBackdrop = false;
	dialog.addEventListener('pointerdown', (e) => {
		pressedOnBackdrop = e.target === dialog;
	});
	dialog.addEventListener('click', (e) => {
		if (pressedOnBackdrop && e.target === dialog) {
			dialog.close();
		}
		pressedOnBackdrop = false;
	});

	// Closing gives the focus back to the button that opened the popup. A
	// clicked control-bar button lets go of it, as the bar's other buttons
	// do, so the bar can fade out over the slides.
	dialog.addEventListener('close', () => {
		const active = document.activeElement;
		if (slidesOpenedByPointer && active && active.closest('#presentTray')) {
			active.blur();
		}
	});

	const current = currentSlidesURL();
	if (current) {
		document.getElementById('slidesButtonText').textContent = 'Change Slides…';
		document.getElementById('slidesButton').setAttribute('aria-label', 'Change slides: paste the address of another deck');
		document.getElementById('slidesTitle').textContent = 'Change Slides';
		document.getElementById('slidesEnd').hidden = false;
		// A deck opened from a link or a bookmark joins the recent ones too.
		rememberSlides(current);
	}
})();
