'use strict';
'require view';
'require poll';
'require rpc';

const callStatus = rpc.declare({
	object: 'simplewan',
	method: 'status',
	expect: { }
});

function render(s) {
	if (!s || s.running === false)
		return E('p', { 'class': 'alert-message warning' },
			_('simplewand is not running (no status file).'));

	const rows = (s.ifaces || []).map(function(i) {
		return E('tr', { 'class': 'tr' }, [
			E('td', { 'class': 'td' }, [
				i.selected ? E('strong', [ i.name + ' ✓' ]) : i.name ]),
			E('td', { 'class': 'td' }, [ i.ifname + ' (' + (i.device || '?') + ')' ]),
			E('td', { 'class': 'td' }, [
				E('span', { 'class': 'label ' + (i.online ? 'success' : 'danger') },
					[ i.online ? _('online') : _('offline') ]) ]),
			E('td', { 'class': 'td' }, [ i.primary ? _('primary') : _('backup') ]),
			E('td', { 'class': 'td' }, [ String(i.metric) ]),
			E('td', { 'class': 'td' }, [ i.has_route ? _('yes') : _('no') ]),
			E('td', { 'class': 'td' }, [ (i.loss_pct != null ? i.loss_pct + '%' : '-') ]),
			E('td', { 'class': 'td' }, [ (i.rtt_ms ? i.rtt_ms + ' ms' : '-') ])
		]);
	});

	return E('div', {}, [
		E('div', { 'class': 'cbi-section' }, [
			E('p', {}, [
				E('strong', [ _('Active upstream: ') ]),
				(s.selected || _('none')),
				'  ',
				E('span', { 'class': 'cbi-value-description' },
					[ _('target') + ': ' + (s.ping_target || '-') ])
			])
		]),
		E('table', { 'class': 'table cbi-section-table' }, [
			E('tr', { 'class': 'tr table-titles' }, [
				E('th', { 'class': 'th' }, [ _('WAN') ]),
				E('th', { 'class': 'th' }, [ _('Interface') ]),
				E('th', { 'class': 'th' }, [ _('Health') ]),
				E('th', { 'class': 'th' }, [ _('Role') ]),
				E('th', { 'class': 'th' }, [ _('Metric') ]),
				E('th', { 'class': 'th' }, [ _('Has route') ]),
				E('th', { 'class': 'th' }, [ _('Loss') ]),
				E('th', { 'class': 'th' }, [ _('RTT') ])
			])
		].concat(rows))
	]);
}

return view.extend({
	load: function() {
		return callStatus().catch(function() { return {}; });
	},

	render: function(data) {
		const holder = E('div', { 'id': 'sw-status' }, [ render(data) ]);

		poll.add(function() {
			return callStatus().then(function(s) {
				const el = document.getElementById('sw-status');
				if (el)
					el.replaceChildren(render(s));
			}).catch(function() {});
		});

		return E('div', {}, [
			E('h2', [ _('SimpleWAN Failover') ]),
			holder
		]);
	},

	handleSave: null,
	handleSaveApply: null,
	handleReset: null
});
