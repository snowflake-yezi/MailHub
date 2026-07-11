(() => {
    const root = document.documentElement;
    const themeToggle = document.getElementById('theme-toggle');
    const password = document.getElementById('password');
    const passwordToggle = document.getElementById('password-toggle');
    const form = document.getElementById('login-form');
    const submit = document.getElementById('login-submit');

    themeToggle?.addEventListener('click', () => {
        const next = root.dataset.theme === 'dark' ? 'light' : 'dark';
        root.dataset.theme = next;
        localStorage.setItem('mailhub.theme', next);
    });

    passwordToggle?.addEventListener('click', () => {
        const show = password.type === 'password';
        password.type = show ? 'text' : 'password';
        passwordToggle.textContent = show ? '◌' : '◉';
        passwordToggle.setAttribute('aria-label', show ? '隐藏密码' : '显示密码');
        passwordToggle.title = show ? '隐藏密码' : '显示密码';
        password.focus();
    });

    form?.addEventListener('submit', () => {
        submit.disabled = true;
        submit.classList.add('is-submitting');
        submit.querySelector('.submit-label').textContent = '正在登录';
    });
})();
