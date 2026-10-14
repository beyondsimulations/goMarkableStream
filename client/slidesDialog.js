// Present Slides: a popup to paste the address of a reveal.js or Quarto deck,
// with the last few decks one click away. It builds the same ?present=
// address as typing it by hand, so bookmarks keep working.

const RECENT_SLIDES_KEY = 'goMarkableStream_recentSlides';
const RECENT_SLIDES_MAX = 5;
const LOCAL_HOSTS = ['localhost', '127.0.0.1', '[::1]'];

// What was pasted, as the address to present ({url}), or what is wrong with
// it ({error}). pageProtocol is this page's, 'https:' or 'http:'.
function normalizeSlidesAddress(text, pageProtocol) {
	let address = text.trim();
	if (!address) {
		return { error: 'Paste the address of the slides.' };
	}
	// An address has no spaces (Chrome would turn one in the host into %20).
	if (/\s/.test(address)) {
		return { error: 'That is not a web address.' };
	}
	// "example.com/talk" means https://example.com/talk.
	if (!/^[a-z][a-z0-9+.-]*:\/\//i.test(address)) {
		address = 'https://' + address;
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
	// from this computer (a Quarto preview, for example).
	if (pageProtocol === 'https:' && url.protocol === 'http:' && !LOCAL_HOSTS.includes(url.hostname)) {
		return { error: 'This page uses https, so the browser does not show http:// slides in it. Use the https:// address of the slides.' };
	}
	return { url: url.href };
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

// The deck shown now, as presentSlides stores it, or null.
function currentSlidesURL() {
	const presentURL = getQueryParam('present');
	return presentURL ? normalizeSlidesAddress(presentURL, window.location.protocol).url || null : null;
}

function presentSlides(url) {
	rememberSlides(url);
	const params = new URLSearchParams(window.location.search);
	params.set('present', url);
	const address = window.location.pathname + '?' + params.toString();
	if (isPresenting()) {
		// Another deck in the same mode: swap the slides in place, so the
		// stream, the touch block and the modes stay as they are.
		history.replaceState(null, '', address);
		iFrame.src = url;
		document.getElementById('slidesDialog').close();
		return;
	}
	window.location.assign(address);
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
	const current = isPresenting() ? currentSlidesURL() : null;
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
		open.addEventListener('click', () => presentSlidesAddress(url));

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

function openSlidesDialog() {
	const dialog = document.getElementById('slidesDialog');
	if (dialog.open) {
		return;
	}
	const presenting = isPresenting();
	document.getElementById('slidesTitle').textContent = presenting ? 'Change Slides' : 'Present Slides';
	document.getElementById('slidesEnd').hidden = !presenting;
	const input = document.getElementById('slidesAddress');
	input.value = presenting ? (getQueryParam('present') || '') : '';
	showSlidesError('');
	renderRecentSlides();
	dialog.showModal();
	// While presenting, the current address is selected, so a paste
	// replaces it.
	input.focus();
	input.select();
}

(function initSlidesDialog() {
	const dialog = document.getElementById('slidesDialog');
	document.getElementById('slidesButton').addEventListener('click', openSlidesDialog);
	document.getElementById('presentSlides').addEventListener('click', openSlidesDialog);
	document.getElementById('slidesForm').addEventListener('submit', (e) => {
		e.preventDefault();
		presentSlidesAddress(document.getElementById('slidesAddress').value);
	});
	document.getElementById('slidesAddress').addEventListener('input', () => showSlidesError(''));
	document.getElementById('slidesCancel').addEventListener('click', () => dialog.close());
	document.getElementById('slidesEnd').addEventListener('click', endPresentation);
	// A click on the dimmed page around the popup closes it (the form fills
	// the dialog, so only the backdrop is the dialog itself).
	dialog.addEventListener('click', (e) => {
		if (e.target === dialog) {
			dialog.close();
		}
	});

	if (getQueryParam('present')) {
		document.getElementById('slidesButtonText').textContent = 'Change Slides…';
		document.getElementById('slidesButton').setAttribute('aria-label', 'Change slides: paste the address of another deck');
		// A deck opened from a link or a bookmark joins the recent ones too.
		const current = currentSlidesURL();
		if (current) {
			rememberSlides(current);
		}
	}
})();
