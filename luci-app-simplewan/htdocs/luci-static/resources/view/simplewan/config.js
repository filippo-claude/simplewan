'use strict';
'require view';
'require form';

return view.extend({
	render: function() {
		let m, s, o;

		m = new form.Map('simplewan', _('SimpleWAN Failover'),
			_('Two-WAN failover by reordering the IPv4 default routes. The daemon never blackholes traffic: if no WAN looks healthy it leaves routing untouched.'));

		s = m.section(form.TypedSection, 'globals', _('Globals'));
		s.anonymous = true;
		s.addremove = false;

		o = s.option(form.Flag, 'enabled', _('Enabled'));
		o.rmempty = false;

		o = s.option(form.Value, 'ping_target', _('Ping target'),
			_('Single IPv4 address pinged out of each WAN.'));
		o.datatype = 'ip4addr';

		o = s.option(form.Value, 'interval', _('Check interval (s)'));
		o.datatype = 'uinteger';

		o = s.option(form.Value, 'timeout', _('Ping timeout (s)'));
		o.datatype = 'uinteger';

		o = s.option(form.Value, 'count', _('Pings per check'));
		o.datatype = 'uinteger';

		o = s.option(form.Value, 'down', _('Failed checks to go offline'));
		o.datatype = 'uinteger';

		o = s.option(form.Value, 'up', _('Good checks to go online'));
		o.datatype = 'uinteger';

		o = s.option(form.Value, 'recovery_time', _('Recovery hold (s)'),
			_('How long a preferred WAN must stay healthy before switching back to it.'));
		o.datatype = 'uinteger';

		o = s.option(form.Flag, 'flush_conntrack', _('Flush conntrack on switch'));

		s = m.section(form.GridSection, 'interface', _('Interfaces'),
			_('Lower priority is preferred. The selected WAN keeps its metric; a demoted WAN is pushed above it.'));
		s.addremove = true;
		s.anonymous = false;
		s.nodescriptions = true;

		o = s.option(form.Value, 'ifname', _('Interface'));
		o.datatype = 'string';

		o = s.option(form.Value, 'metric', _('Metric'));
		o.datatype = 'uinteger';

		o = s.option(form.Value, 'priority', _('Priority'));
		o.datatype = 'uinteger';

		s = m.section(form.NamedSection, 'notify', 'notify', _('Email notifications (Postmark)'));
		s.addremove = false;

		o = s.option(form.Flag, 'enabled', _('Enabled'));

		o = s.option(form.Value, 'postmark_token', _('Postmark server token'));
		o.password = true;

		o = s.option(form.Value, 'mail_from', _('From (verified sender)'));
		o = s.option(form.Value, 'mail_to', _('To'));
		o = s.option(form.Value, 'subject_prefix', _('Subject prefix'));

		return m.render();
	}
});
