(() => {
  const input = document.getElementById('handle');
  const result = document.getElementById('handle-check');
  if (!input || !result) return;
  let timer;
  let pending;
  input.addEventListener('input', () => {
    clearTimeout(timer);
    if (pending) pending.abort();
    input.setCustomValidity('');
    result.textContent = '';
    if (!input.value || !input.validity.valid) return;
    const value = input.value;
    timer = setTimeout(async () => {
      pending = new AbortController();
      try {
        const response = await fetch(`/api/v1/handle/check?h=${encodeURIComponent(value)}`, {signal: pending.signal});
        if (!response.ok) throw new Error('unavailable');
        const check = await response.json();
        if (input.value !== value) return;
        const message = check.available ? 'Handle available.' : check.reason === 'handle_invalid'
          ? 'Use 6–15 letters and digits, starting with a letter.'
          : 'That handle is unavailable. Choose another.';
        result.textContent = message;
        input.setCustomValidity(check.available ? '' : message);
      } catch (error) {
        if (error.name !== 'AbortError' && input.value === value) result.textContent = 'Availability will be checked when you submit.';
      }
    }, 500);
  });
})();
